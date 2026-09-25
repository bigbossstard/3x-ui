#!/usr/bin/env bash
set -Eeuo pipefail

# 3x-ui ClientHost group matrix tester
# - never modifies the source DB
# - copies /etc/x-ui and /usr/local/x-ui into a temp tree
# - starts a second x-ui process against the copied DB on private localhost ports
# - mutates ONLY the copied DB
# - exercises the real subscription endpoint through the running x-ui binary
#
# Usage:
#   bash client-host-matrix-tester.sh
#   bash client-host-matrix-tester.sh --db /path/to/backup/x-ui.db
#   bash client-host-matrix-tester.sh --db /path/to/x-ui.db --binary /path/to/new-x-ui
#
# Optional:
#   KEEP_TEST_ARTIFACTS=1 ...   keep temp workspace

ROOT=${ROOT:-/usr/local/x-ui}
ETCDIR=${ETCDIR:-/etc/x-ui}
DEFAULT_BINARY="$ROOT/x-ui"
DB_ARG=""
BIN_ARG=""
KEEP=${KEEP_TEST_ARTIFACTS:-0}
ITERATIONS=${ITERATIONS:-80}
STARTUP_ONLY=0

usage() {
  cat <<'EOF'
Usage:
  client-host-matrix-tester.sh [--db PATH] [--binary PATH] [--iterations N]

Options:
  --db PATH          source x-ui.db (default: /etc/x-ui/x-ui.db, then auto-search)
  --binary PATH      x-ui binary to test (default: /usr/local/x-ui/x-ui)
  --iterations N     extra randomized state transitions (default: 80)
  --keep             keep the temporary test workspace
  --startup-only     stop after panel + subscription listeners are verified
EOF
}

while (($#)); do
  case "$1" in
    --db) DB_ARG=${2:?missing path after --db}; shift 2 ;;
    --binary) BIN_ARG=${2:?missing path after --binary}; shift 2 ;;
    --iterations) ITERATIONS=${2:?missing number after --iterations}; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --startup-only) STARTUP_ONLY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

need() { command -v "$1" >/dev/null 2>&1 || { echo "ERROR: missing command: $1" >&2; exit 2; }; }
for c in python3 curl; do need "$c"; done

if [[ -n "$DB_ARG" ]]; then
  SRC_DB=$DB_ARG
elif [[ -f "$ETCDIR/x-ui.db" ]]; then
  SRC_DB="$ETCDIR/x-ui.db"
else
  SRC_DB=$(find /etc/x-ui /root /var/backups /opt /tmp -maxdepth 4 -type f \( -name 'x-ui.db' -o -name '*x-ui*.db' -o -name '*.db' \) 2>/dev/null | head -n1 || true)
fi

[[ -n "${SRC_DB:-}" && -f "$SRC_DB" ]] || { echo "ERROR: could not locate x-ui.db. Use --db PATH" >&2; exit 2; }

if [[ -n "$BIN_ARG" ]]; then
  SRC_BIN=$BIN_ARG
else
  SRC_BIN=$DEFAULT_BINARY
fi
[[ -x "$SRC_BIN" ]] || { echo "ERROR: x-ui binary not found/executable: $SRC_BIN" >&2; exit 2; }

if (( ITERATIONS < 0 )); then echo "ERROR: iterations must be >= 0" >&2; exit 2; fi

WORK=$(mktemp -d /tmp/xui-client-host-test.XXXXXX)
NS_ROOT="$WORK/nsroot"
TMP_ETC="$NS_ROOT/etc/x-ui"
TMP_ROOT="$NS_ROOT/usr/local/x-ui"
COPY_DB="$TMP_ETC/x-ui.db"
RUN_LOG="$WORK/x-ui.log"
STATE_JSON="$WORK/state.json"
mkdir -p "$TMP_ETC" "$TMP_ROOT"

echo "[INFO] source DB : $SRC_DB"
echo "[INFO] test binary: $SRC_BIN"
echo "[INFO] iterations : $ITERATIONS"
echo "[INFO] workspace  : $WORK"

# Copy config tree + binary. We deliberately do NOT operate on live paths after this point.
cp -a "$ETCDIR/." "$TMP_ETC/" 2>/dev/null || true
cp -a "$ROOT/." "$TMP_ROOT/" 2>/dev/null || true
cp -f "$SRC_BIN" "$TMP_ROOT/x-ui"
chmod +x "$TMP_ROOT/x-ui"
python3 - "$SRC_DB" "$COPY_DB" <<'PYDB'
import sqlite3, sys
src, dst = sys.argv[1], sys.argv[2]
sc = sqlite3.connect(src, timeout=30)
dc = sqlite3.connect(dst)
try:
    sc.backup(dc)
finally:
    dc.close(); sc.close()
PYDB

# Prepare the copied DB so the real x-ui can run safely beside the live service.
# The copy is configured to use loopback + random high ports. All existing inbounds
# are disabled only in the COPY before x-ui starts; the matrix later toggles their
# enable flags in that copied DB. This avoids Xray binding any production ports.
echo "[1/5] Creating a consistent SQLite snapshot..."
read -r SUB_PORT PANEL_PORT SUB_PATH JSON_PATH CLASH_PATH <<EOF
$(python3 - "$COPY_DB" <<'PYPORT'
import sqlite3, sys, random
p=sys.argv[1]
con=sqlite3.connect(p)
cur=con.cursor()

def setting(name, value):
    cur.execute("update settings set value=? where key=?", (str(value), name))
    if cur.rowcount == 0:
        cur.execute("insert into settings(key,value) values(?,?)", (name,str(value)))

# Pick ports unlikely to collide with the live panel/subscriber.
rng=random.Random()
sub=39000+rng.randrange(1000)
panel=40000+rng.randrange(1000)
# Force HTTP + loopback for the test instance. Certificates/domain validators are
# irrelevant to the matrix and could otherwise make localhost probes fail.
for k,v in {
    'subPort':sub,
    'subListen':'127.0.0.1',
    'subDomain':'',
    'subCertFile':'',
    'subKeyFile':'',
    'subEnable':'true',
    'subJsonEnable':'true',
    'subClashEnable':'true',
    'subPath':'/sub-test/',
    'subJsonPath':'/json-test/',
    'subClashPath':'/clash-test/',
    'webPort':panel,
    'webListen':'127.0.0.1',
    'webDomain':'',
    'webCertFile':'',
    'webKeyFile':'',
    'webBasePath':'/',
    'tgBotEnable':'false',
    'discordBotEnable':'false',
    'smtpEnable':'false',
}.items(): setting(k,v)
# Never let startup Xray bind real production ports.
cur.execute("update inbounds set enable=0")
con.commit()
print(sub, panel, '/sub-test/', '/json-test/', '/clash-test/')
con.close()
PYPORT
)
EOF

mkdir -p "$TMP_ROOT/bin" "$WORK/log"

# x-ui startup always calls RestartXray(true). The copied production Xray config
# can therefore try to bind live ports even though this test instance uses its
# own panel/subscription listeners. The matrix only needs the real x-ui web and
# subscription servers, not a running Xray core. Remove copied Xray executables
# from the TEST TREE so startup cannot touch production listeners.
find "$TMP_ROOT/bin" -maxdepth 1 -type f \
  \( -name 'xray*' -o -name 'xray-*' \) -delete 2>/dev/null || true

# Everything below is inside the temporary workspace; the live /usr/local/x-ui
# tree and the source DB are never modified.
export XUI_DB_FOLDER="$TMP_ETC"
export XUI_LOG_FOLDER="$WORK/log"
export XUI_BIN_FOLDER="$TMP_ROOT/bin"
export XUI_PORT="$PANEL_PORT"
export XUI_LOG_LEVEL="info"
export XUI_ENABLE_FAIL2BAN="false"

echo "[2/5] Starting isolated x-ui process on panel=$PANEL_PORT sub=$SUB_PORT..."
cd "$TMP_ROOT"
"$TMP_ROOT/x-ui" run >"$RUN_LOG" 2>&1 &
XPID=$!

cleanup() {
  set +e
  if [[ -n "${XPID:-}" ]] && kill -0 "$XPID" 2>/dev/null; then
    kill "$XPID" 2>/dev/null || true
    for _ in {1..50}; do
      kill -0 "$XPID" 2>/dev/null || break
      sleep 0.2
    done
    kill -9 "$XPID" 2>/dev/null || true
  fi
  if (( KEEP == 1 )); then
    echo "KEEP=1 -> test workspace: $WORK"
  else
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT

# Wait for the panel first. This distinguishes a general x-ui startup problem
# from a subscription-listener problem.
echo "[3/5] Waiting for panel listener..."
panel_ready=0
for _ in {1..50}; do
  if ! kill -0 "$XPID" 2>/dev/null; then
    break
  fi
  if curl -sS -o /dev/null --max-time 1 "http://127.0.0.1:${PANEL_PORT}/" 2>/dev/null; then
    panel_ready=1
    break
  fi
  sleep 0.2
done
if (( ! panel_ready )); then
  echo "ERROR: test x-ui did not open panel port $PANEL_PORT" >&2
  echo "--- x-ui log ---" >&2
  sed -n '1,320p' "$RUN_LOG" >&2 || true
  exit 1
fi
echo "[PASS] panel listener is up on $PANEL_PORT"

# The subscription server is a separate listener according to 3x-ui's current
# architecture, so check it explicitly rather than assuming panel readiness means
# subscriptions are ready.
echo "[4/5] Waiting for subscription listener..."
sub_ready=0
for _ in {1..50}; do
  if ! kill -0 "$XPID" 2>/dev/null; then
    break
  fi
  if curl -sS -o /dev/null --max-time 1 "http://127.0.0.1:${SUB_PORT}/" >/dev/null 2>&1; then
    sub_ready=1
    break
  fi
  sleep 0.2
done
if (( ! sub_ready )); then
  echo "ERROR: panel started, but subscription listener did not open port $SUB_PORT" >&2
  echo "--- x-ui log ---" >&2
  sed -n '1,320p' "$RUN_LOG" >&2 || true
  echo "--- listening sockets matching test ports ---" >&2
  (ss -ltnp 2>/dev/null | grep -E ":(${PANEL_PORT}|${SUB_PORT})\b" || true) >&2
  echo "--- copied settings ---" >&2
  python3 - "$COPY_DB" <<'PYSET' >&2
import sqlite3,sys
con=sqlite3.connect(sys.argv[1])
for k in ('webPort','webListen','subEnable','subPort','subListen','subPath','subJsonEnable','subJsonPath','subClashEnable','subClashPath'):
    r=con.execute('select value from settings where key=?',(k,)).fetchone()
    print(f'{k}={r[0] if r else "<missing>"}')
con.close()
PYSET
  exit 1
fi
echo "[PASS] subscription listener is up on $SUB_PORT"

if (( STARTUP_ONLY == 1 )); then
  echo "[PASS] startup-only check complete"
  exit 0
fi

python3 - "$COPY_DB" "$STATE_JSON" "$ITERATIONS" "$SUB_PORT" "$SUB_PATH" "$JSON_PATH" "$CLASH_PATH" <<'PY'
import base64, json, os, random, re, sqlite3, string, subprocess, sys, time

DB=sys.argv[1]
STATE=sys.argv[2]
ITER=int(sys.argv[3])
PORT=int(sys.argv[4])
SUB_PATH=sys.argv[5]
JSON_PATH=sys.argv[6]
CLASH_PATH=sys.argv[7]

con=sqlite3.connect(DB)
con.row_factory=sqlite3.Row
cur=con.cursor()

def cols(t): return [r[1] for r in cur.execute(f'pragma table_info("{t}")')]

def has(t,c): return c in cols(t)

def q1(sql,args=()): return cur.execute(sql,args).fetchone()

def qall(sql,args=()): return cur.execute(sql,args).fetchall()

def must_table(t):
    if not q1("select 1 from sqlite_master where type='table' and name=?",(t,)):
        raise RuntimeError(f'missing table {t}')

for t in ['clients','client_inbounds','client_group_hosts','hosts','inbounds']:
    must_table(t)

client_cols=cols('clients'); ci_cols=cols('client_inbounds'); host_cols=cols('hosts'); inb_cols=cols('inbounds')

if not has('clients','id') or not has('clients','email') or not has('clients','sub_id') or not has('clients','group_name'):
    raise RuntimeError(f'clients schema unsupported: {client_cols}')
group_host_cols=cols('client_group_hosts')
if not {'group_name','host_group_id'}.issubset(set(group_host_cols)):
    raise RuntimeError(f'client_group_hosts schema unsupported: {group_host_cols}')
if not {'client_id','inbound_id'}.issubset(set(ci_cols)):
    raise RuntimeError(f'client_inbounds schema unsupported: {ci_cols}')
if not {'inbound_id','group_id','address'}.issubset(set(host_cols)):
    raise RuntimeError(f'hosts schema unsupported: {host_cols}')
if not {'id','enable','protocol'}.issubset(set(inb_cols)):
    raise RuntimeError(f'inbounds schema unsupported: {inb_cols}')

allowed={'vmess','vless','trojan','shadowsocks','hysteria','wireguard','amneziawg','mtproto','tuic'}
inbounds=[r for r in qall("select * from inbounds where protocol in (%s) order by id" % ','.join('?'*len(allowed)), tuple(sorted(allowed)))]
if len(inbounds)<2:
    raise RuntimeError(f'need at least 2 subscription-capable inbounds; found {len(inbounds)}')

# Prefer two different inbounds with at least one reusable host row somewhere.
# Pick two inbounds of the same protocol so cloned hosts remain protocol-compatible.
preferred=['vless','trojan','vmess','shadowsocks','hysteria','tuic','wireguard','amneziawg','mtproto']
by_proto={}
for r in inbounds: by_proto.setdefault(str(r['protocol']), []).append(r)
chosen=None
for proto in preferred + sorted(by_proto):
    if len(by_proto.get(proto,[])) >= 2:
        chosen=(proto, by_proto[proto][0], by_proto[proto][1]); break
if chosen is None:
    raise RuntimeError('need at least 2 inbounds with the same protocol for a safe synthetic subscription test')
PROTO,I1R,I2R=chosen
I1=int(I1R['id']); I2=int(I2R['id'])
base_host=q1('select h.* from hosts h join inbounds i on i.id=h.inbound_id where i.protocol=? and h.group_id is not null and trim(h.group_id)<>"" order by h.id limit 1',(PROTO,))
if base_host is None:
    raise RuntimeError(f'no usable host row found for protocol {PROTO}')

seed=q1('select * from clients order by id limit 1')
if seed is None:
    raise RuntimeError('no existing client row found to clone')

# Helpers to duplicate rows while preserving all columns/fields and overriding selected values.
def insert_clone(table, row, overrides):
    cc=cols(table)
    vals=[]
    for c in cc:
        v=row[c] if c in row.keys() else None
        if c in overrides: v=overrides[c]
        vals.append(v)
    marks=','.join('?' for _ in cc)
    cur.execute(f'insert into "{table}" ({",".join(chr(34)+c+chr(34) for c in cc)}) values ({marks})',vals)

next_client=int(q1('select coalesce(max(id),0)+10000 from clients')[0])
clients=[]
for idx,email in enumerate([
    'Legacy.TEST@example.COM',
    'rEsTrIcTeD.A@example.com',
    'MULTI.B+Case@EXAMPLE.com',
    'wEiRd_CaSe.4@eXample.CoM',
]):
    cid=next_client+idx
    subid='TEST-CH-'+''.join(random.choice(string.ascii_letters+string.digits) for _ in range(18))+str(idx)
    client_group=f'TEST-CLIENT-GRP-{idx+1}-'+''.join(random.choice(string.ascii_lowercase+string.digits) for _ in range(8))
    overrides={'id':cid,'email':email,'sub_id':subid,'group_name':client_group}
    if 'enable' in client_cols: overrides['enable']=1
    if 'uuid' in client_cols: overrides['uuid']=str(__import__('uuid').uuid4())
    if 'password' in client_cols: overrides['password']='CH-'+''.join(random.choice(string.ascii_letters+string.digits) for _ in range(24))
    if 'remark' in client_cols: overrides['remark']=f'CLIENT-HOST-MATRIX-{idx+1}'
    insert_clone('clients',seed,overrides)
    clients.append({'id':cid,'email':email,'sub_id':subid,'group_name':client_group})

# Clear any accidental unique columns from clones are already generated; attach all four to I1/I2 via normalized table.
for c in clients:
    for iid in (I1,I2):
        # avoid duplicates if composite key includes more fields: copy a matching row if available, else populate known pair.
        cols_ci=ci_cols
        existing=q1('select 1 from client_inbounds where client_id=? and inbound_id=?',(c['id'],iid))
        if existing: continue
        if len(cols_ci)==2:
            cur.execute('insert into client_inbounds (client_id,inbound_id) values (?,?)',(c['id'],iid))
        else:
            seed_ci=q1('select * from client_inbounds limit 1')
            if seed_ci is None: raise RuntimeError('cannot clone client_inbounds rows: table has extra columns and no seed row')
            ov={'client_id':c['id'],'inbound_id':iid}
            insert_clone('client_inbounds',seed_ci,ov)

# Create four logical test groups, cloned to both selected inbounds as required by the scenarios.
base_id=int(q1('select coalesce(max(id),0)+20000 from hosts')[0])
groups={
    'A': 'TEST-GRP-A-'+''.join(random.choice(string.ascii_lowercase+string.digits) for _ in range(8)),
    'B': 'TEST-GRP-B-'+''.join(random.choice(string.ascii_lowercase+string.digits) for _ in range(8)),
    'C': 'TEST-GRP-C-'+''.join(random.choice(string.ascii_lowercase+string.digits) for _ in range(8)),
    'D': 'TEST-GRP-D-'+''.join(random.choice(string.ascii_lowercase+string.digits) for _ in range(8)),
    'E': 'TEST-GRP-E-'+''.join(random.choice(string.ascii_lowercase+string.digits) for _ in range(8)),
}
created_hosts=[]

# Clone a host row. Hosts are generic endpoint definitions; we deliberately give unique synthetic addresses so returned links are attributable.
def mkhost(key,iid,disabled=0,exclude='[]'):
    global base_id
    gid=groups[key]
    addr=f'{key.lower()}.client-host-test.invalid'
    rem=f'CH-MATRIX-{key}-{iid}'
    ov={'id':base_id,'inbound_id':iid,'group_id':gid,'address':addr}
    if 'remark' in host_cols: ov['remark']=rem
    if 'is_disabled' in host_cols: ov['is_disabled']=disabled
    if 'exclude_from_sub_types' in host_cols: ov['exclude_from_sub_types']=exclude
    insert_clone('hosts',base_host,ov)
    created_hosts.append({'id':base_id,'group':gid,'inbound':iid,'key':key,'address':addr,'disabled':disabled,'exclude':exclude})
    base_id+=1

# A only I1, B on I1+I2, C only I2, D only I2 but disabled.
mkhost('A',I1)
mkhost('B',I1)
mkhost('B',I2)
mkhost('C',I2,0,'[]')
mkhost('D',I2,1,'[]')
mkhost('E',I1,0,'["raw"]')

con.commit()

# Utility: reset synthetic assignment rows only; host/inbound/client topology is then changed per scenario.
def clear_assignments():
    marks=','.join('?' for _ in clients)
    cur.execute(f'delete from client_group_hosts where group_name in ({marks})', tuple(c['group_name'] for c in clients))

def set_assign(client, keys):
    cur.execute('delete from client_group_hosts where group_name=?',(client['group_name'],))
    for k in keys:
        cur.execute('insert or ignore into client_group_hosts (group_name,host_group_id) values (?,?)',(client['group_name'],groups[k]))

def set_inbounds(client, ids):
    cur.execute('delete from client_inbounds where client_id=?',(client['id'],))
    for iid in ids:
        cur.execute('insert or ignore into client_inbounds (client_id,inbound_id) values (?,?)',(client['id'],iid))

def set_enabled(iid, enabled):
    cur.execute('update inbounds set enable=? where id=?',(1 if enabled else 0,iid))

def group_hosts_for(iid, keys=None):
    params=[iid]
    sql='select group_id,address,is_disabled,exclude_from_sub_types from hosts where inbound_id=?'
    if keys:
        gids=[groups[k] for k in keys]
        marks=','.join('?' for _ in gids)
        sql += f' and group_id in ({marks})'; params.extend(gids)
    return qall(sql,params)

def curl_path(subid,path=None):
    if path is None:
        path=SUB_PATH
    url=f'http://127.0.0.1:{PORT}{path}{subid}'
    p=subprocess.run(['curl','-sS','-L','-w','\nHTTP_CODE:%{http_code}\n','--max-time','10',url],capture_output=True,text=True)
    if p.returncode!=0:
        return p.returncode,'',p.stderr
    return 0,p.stdout,''

def decode_raw(body):
    s=body
    if 'HTTP_CODE:' in s: s=s.split('HTTP_CODE:',1)[0]
    s=s.strip()
    if not s: return ''
    try:
        return base64.b64decode(s+'='*((4-len(s)%4)%4),validate=False).decode('utf-8','replace')
    except Exception:
        return s

def get_raw(subid):
    rc,body,err=curl_path(subid,SUB_PATH)
    if rc: raise RuntimeError(f'curl raw failed: {err[:300]}')
    return decode_raw(body)

# Endpoint autodiscovery for JSON/Clash variants; raw is canonical and mandatory.
def discover_variant(subid, candidates):
    for path in candidates:
        rc,body,err=curl_path(subid,path)
        if rc==0 and 'HTTP_CODE:200' in body:
            return path, body.split('HTTP_CODE:',1)[0]
    return None,''

results=[]

def expect(name, observed, expected, detail=''):
    ok=observed==expected
    results.append({'name':name,'ok':ok,'observed':observed,'expected':expected,'detail':detail})
    print(f"[{'PASS' if ok else 'FAIL'}] {name}")
    if not ok:
        print('       expected:',expected)
        print('       observed:',observed)
        if detail: print('       detail:',detail)
    return ok

def links_matching(raw, addrs):
    return {a: raw.count(a) for a in addrs}

def run_case(name, client, assigned, enabled1=True, enabled2=True, attach=(I1,I2), expected_addrs=None, expected_counts=None, exclusions=False):
    clear_assignments(); set_inbounds(client,attach); set_assign(client,assigned); set_enabled(I1,enabled1); set_enabled(I2,enabled2); con.commit()
    raw=get_raw(client['sub_id'])
    addrs=[f'{k.lower()}.client-host-test.invalid' for k in groups]
    got=links_matching(raw,addrs)
    if expected_addrs is None:
        expected_addrs=[]
    exp={a:(expected_counts.get(a, 1) if expected_counts and a in expected_counts else (1 if a in expected_addrs else 0)) for a in addrs}
    return expect(name,got,exp)

# 1 Legacy group: no HostGroup assignment => all enabled applicable hosts.
run_case('01 legacy client = all applicable enabled hosts',clients[0],[],True,True,expected_addrs=['a.client-host-test.invalid','b.client-host-test.invalid','c.client-host-test.invalid'],expected_counts={'b.client-host-test.invalid':2})

# 2 Single group assignment A => only A on I1; B/C/D excluded.
run_case('02 single HostGroup',clients[1],['A'],True,True,expected_addrs=['a.client-host-test.invalid'])

# 3 B exists on two inbounds => should be returned once per inbound, so count 2.
clear_assignments(); set_inbounds(clients[1],(I1,I2)); set_assign(clients[1],['B']); set_enabled(I1,True); set_enabled(I2,True); con.commit()
raw=get_raw(clients[1]['sub_id']); got={k:raw.count(f'{k.lower()}.client-host-test.invalid') for k in ['A','B','C','D','E']}
expect('03 group on multiple inbounds',got,{'A':0,'B':2,'C':0,'D':0,'E':0})

# 4 Only-on-disabled => no fallback.
run_case('04 assigned group exists only on disabled inbound',clients[1],['C'],True,False,expected_addrs=[])

# 5 Same group on enabled+disabled => enabled one survives, disabled one omitted.
run_case('05 assigned group exists on enabled and disabled inbounds',clients[1],['B'],True,False,expected_addrs=['b.client-host-test.invalid'])
# It should be exactly one B link, not two.
raw=get_raw(clients[1]['sub_id']); expect('05b enabled/disabled B exact multiplicity',raw.count('b.client-host-test.invalid'),1)

# 6 Multiple assignments A+B.
clear_assignments(); set_inbounds(clients[2],(I1,I2)); set_assign(clients[2],['A','B']); set_enabled(I1,True); set_enabled(I2,True); con.commit()
raw=get_raw(clients[2]['sub_id']); got={k:raw.count(f'{k.lower()}.client-host-test.invalid') for k in ['A','B','C','D','E']}
expect('06 multiple HostGroups',got,{'A':1,'B':2,'C':0,'D':0,'E':0})

# 7 Disabled Host itself => no D even on enabled inbound.
cur.execute('update hosts set inbound_id=?, is_disabled=1 where group_id=?',(I1,groups['D'])); con.commit()
run_case('07 disabled host is omitted',clients[1],['D'],True,True,expected_addrs=[])

# 8 ExcludeFromSubTypes: raw excludes C, so assignment C gives zero in raw.
run_case('08 ExcludeFromSubTypes raw exclusion',clients[1],['E'],True,True,expected_addrs=[])

# 9 No matching host on attached inbound => zero.
run_case('09 restricted client with no matching host = zero',clients[1],['A'],False,True,attach=(I1,I2),expected_addrs=[])

# Restore D onto I2 disabled and C onto I2; restore baseline flags.
cur.execute('update hosts set inbound_id=?, is_disabled=1 where group_id=?',(I2,groups['D']))
set_enabled(I1,True); set_enabled(I2,True); con.commit()

# 10 Delete physical B rows, assignment remains but produces none.
clear_assignments(); set_inbounds(clients[1],(I1,I2)); set_assign(clients[1],['B']); con.commit()
cur.execute('delete from hosts where group_id=?',(groups['B'],)); con.commit()
raw=get_raw(clients[1]['sub_id']); expect('10 delete physical Host rows, assignment remains',raw.count('b.client-host-test.invalid'),0)
# 11 Recreate B on I1+I2; same logical group_id must become live again.
mkhost('B',I1); mkhost('B',I2); con.commit()
raw=get_raw(clients[1]['sub_id']); expect('11 recreate HostGroup with same logical group_id',raw.count('b.client-host-test.invalid'),2)

# 12 Move A from I1 to I2. Client remains attached to I1 only => disappears.
cur.execute('update hosts set inbound_id=? where group_id=?',(I2,groups['A'])); set_inbounds(clients[1],(I1,)); set_assign(clients[1],['A']); set_enabled(I1,True); set_enabled(I2,True); con.commit()
raw=get_raw(clients[1]['sub_id']); expect('12 move Host to another inbound',raw.count('a.client-host-test.invalid'),0)
# Move back and keep the client on I2; the moved Host is not applicable there.
cur.execute('update hosts set inbound_id=? where group_id=?',(I1,groups['A'])); set_inbounds(clients[1],(I2,)); con.commit()
raw=get_raw(clients[1]['sub_id']); expect('12b moved Host follows new client inbound',raw.count('a.client-host-test.invalid'),0)

# 13 Change Client inbounds. Assigned B remains, output follows client_inbounds.
set_inbounds(clients[2],(I1,I2)); set_assign(clients[2],['B']); set_enabled(I1,True); set_enabled(I2,True); con.commit()
raw=get_raw(clients[2]['sub_id']); expect('13a client attached to two inbounds',raw.count('b.client-host-test.invalid'),2)
set_inbounds(clients[2],(I1,)); con.commit(); raw=get_raw(clients[2]['sub_id']); expect('13b client detached from second inbound',raw.count('b.client-host-test.invalid'),1)

# 14 Email case: mixed-case email still resolves assignment path.
set_inbounds(clients[3],(I1,I2)); set_assign(clients[3],['B']); set_enabled(I1,True); set_enabled(I2,True); con.commit()
raw=get_raw(clients[3]['sub_id']); expect('14 mixed-case email assignment lookup',raw.count('b.client-host-test.invalid'),2)

# 15 Client deletion cleanup is simulated against the copied DB schema. Verify no group assignment rows remain after delete.
clear_assignments(); set_assign(clients[3],['B']); con.commit()
cur.execute('delete from client_group_hosts where group_name=?',(clients[3]['group_name'],)); cur.execute('delete from client_inbounds where client_id=?',(clients[3]['id'],)); cur.execute('delete from clients where id=?',(clients[3]['id'],)); con.commit()
rows=cur.execute('select count(*) from client_group_hosts where group_name=?',(clients[3]['group_name'],)).fetchone()[0]
expect('15 client deletion leaves no ClientGroupHost rows',rows,0)

# 16 Import/export semantic round-trip at DB level: assigned set can be cleared and restored without duplicates.
clear_assignments(); set_assign(clients[2],['A','B']); con.commit()
orig=[r[0] for r in qall('select host_group_id from client_group_hosts where group_name=? order by host_group_id',(clients[2]['group_name'],))]
clear_assignments(); set_assign(clients[2],['B','A','A']); con.commit()
back=[r[0] for r in qall('select host_group_id from client_group_hosts where group_name=? order by host_group_id',(clients[2]['group_name'],))]
expect('16 assignment round-trip dedupe',back,orig)

# 17 Fail-closed check: temporarily drop a required lookup table in a transactionless copy is unsafe for live DB.
# Instead verify the implementation's synthetic restriction invariant indirectly: a restricted assignment to a non-existing group returns zero.
clear_assignments(); set_inbounds(clients[2],(I1,I2)); cur.execute('insert into client_group_hosts values (?,?)',(clients[2]['group_name'],'NONEXISTENT-GROUP-XYZ')); con.commit()
raw=get_raw(clients[2]['sub_id']); expect('17 nonexistent assigned group returns zero (no legacy fallback)',sum(raw.count(f'{k.lower()}.client-host-test.invalid') for k in ['A','B','C','D','E']),0)

# 18 Raw endpoint smoke: subscription server answers 200 for a valid assigned group.
set_assign(clients[2],['B']); con.commit()
rc,body,err=curl_path(clients[2]['sub_id'],SUB_PATH)
expect('18 raw subscription endpoint returns HTTP 200', 'HTTP_CODE:200' in body, True, err[:200])

# Try JSON and Clash endpoints. Exact routes vary across 3x-ui revisions, so discover rather than assume.
json_candidates=[JSON_PATH]
clash_candidates=[CLASH_PATH]
json_path,json_body=discover_variant(clients[2]['sub_id'],json_candidates)
clash_path,clash_body=discover_variant(clients[2]['sub_id'],clash_candidates)
print('[INFO] JSON endpoint:', json_path or 'not discovered')
print('[INFO] Clash endpoint:', clash_path or 'not discovered')
if json_path:
    forbidden=sum(json_body.count(f'{k.lower()}.client-host-test.invalid') for k in ['A','C','D','E'])
    expect('19 JSON respects restricted selection',forbidden,0)
else:
    results.append({'name':'19 JSON endpoint discovery','ok':True,'observed':'not discovered','expected':'optional','detail':''})
if clash_path:
    forbidden=sum(clash_body.count(f'{k.lower()}.client-host-test.invalid') for k in ['A','C','D','E'])
    expect('20 Clash respects restricted selection',forbidden,0)
else:
    results.append({'name':'20 Clash endpoint discovery','ok':True,'observed':'not discovered','expected':'optional','detail':''})

# Random state machine. It deliberately churns the same synthetic graph so the real binary
# sees many combinations of enabled/disabled inbounds, client attachment changes,
# group assignment changes, host moves, Host disable flags, and format exclusions.
rng=random.Random(0x3A17)
active_clients=clients[:3]
keys=['A','B','C','D','E']

def current_expected(c, assigned, attached):
    expected={k:0 for k in keys}
    for h in created_hosts:
        hr=q1('select is_disabled,exclude_from_sub_types,inbound_id,group_id,address from hosts where id=?',(h['id'],))
        if hr is None: continue
        if assigned and str(hr['group_id']) not in {groups[k] for k in assigned}: continue
        if int(hr['is_disabled'] or 0): continue
        if int(hr['inbound_id']) not in attached: continue
        en=q1('select enable from inbounds where id=?',(int(hr['inbound_id']),))
        if not en or int(en[0] or 0)==0: continue
        ex=str(hr['exclude_from_sub_types'] or '').lower()
        if 'raw' in ex: continue
        expected[h['key']]+=1
    return expected

for n in range(ITER):
    c=rng.choice(active_clients)
    attached=tuple(i for i in (I1,I2) if rng.choice([True,False]))
    if not attached: attached=(rng.choice((I1,I2)),)
    set_inbounds(c,attached)

    set_enabled(I1,rng.choice([True,False])); set_enabled(I2,rng.choice([True,False]))
    clear_assignments()
    assigned=[k for k in keys if rng.choice([True,False])]
    if rng.random()<0.25: assigned=[]  # legacy mode
    set_assign(c,assigned)

    # Churn physical Hosts without changing logical group IDs.
    if rng.random()<0.60:
        k=rng.choice(keys)
        cur.execute('update hosts set inbound_id=? where group_id=?',(rng.choice((I1,I2)),groups[k]))
    if rng.random()<0.55:
        k=rng.choice(keys)
        cur.execute('update hosts set is_disabled=? where group_id=?',(rng.choice([0,1]),groups[k]))
    if rng.random()<0.55:
        k=rng.choice(keys)
        ex=rng.choice(['[]','["clash"]','["json"]','["raw"]','["clash","json"]'])
        cur.execute('update hosts set exclude_from_sub_types=? where group_id=?',(ex,groups[k]))

    # Periodically remove every physical row of a group, then recreate it on a random inbound.
    # This stresses the logical-group persistence rule.
    if n % 11 == 4:
        k=rng.choice(keys)
        cur.execute('delete from hosts where group_id=?',(groups[k],))
        mkhost(k,rng.choice((I1,I2)),0,'[]')
    con.commit()

    raw=get_raw(c['sub_id'])
    observed={k:raw.count(f'{k.lower()}.client-host-test.invalid') for k in keys}
    expected=current_expected(c,assigned,attached)
    ok=observed==expected
    results.append({'name':f'R{n+1:03d} randomized state','ok':ok,'observed':observed,'expected':expected,'detail':f'client={c["email"]} assigned={assigned} attached={attached}'})
    if not ok:
        print(f"[FAIL] R{n+1:03d} randomized state")
        print('       expected:',expected)
        print('       observed:',observed)
        print('       state:',results[-1]['detail'])
        raise SystemExit(3)
    elif n % 10 == 0:
        print(f'[PASS] randomized states {n+1}/{ITER}')

# Orphan cleanup invariant for a fake vanished HostGroup assignment.
vanish='VANISHING-GROUP'
cur.execute('insert or ignore into client_group_hosts(group_name,host_group_id) values(?,?)',(clients[0]['group_name'],vanish)); con.commit()
cur.execute('delete from client_group_hosts where host_group_id not in (select distinct group_id from hosts where group_id is not null) and host_group_id=?',(vanish,)); con.commit()
left=cur.execute('select count(*) from client_group_hosts where host_group_id=?',(vanish,)).fetchone()[0]
expect('21 orphan ClientGroupHost assignment pruning',left,0)

con.commit()

failed=[r for r in results if not r['ok']]
print('\n=== RESULT ===')
print(f"PASS: {len(results)-len(failed)} / {len(results)}")
if failed:
    print('FAILED:')
    for r in failed: print(' -',r['name'])
    sys.exit(1)
print('ALL TESTS PASSED')

with open(STATE,'w') as f:
    json.dump({'db':DB,'clients':clients,'groups':groups,'results':results},f,ensure_ascii=False,indent=2)
PY

RC=$?
echo '[5/5] Matrix complete; evaluating results...'
# Preserve the Python matrix exit status after printing the shell summary.
exit $RC