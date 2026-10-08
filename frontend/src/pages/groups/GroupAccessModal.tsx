import { useEffect, useMemo, useState } from 'react';
import { Alert, Button, Modal, Select, Space, Spin, Typography, message } from 'antd';
import { useTranslation } from 'react-i18next';

import type { GroupSummary, InboundOption } from '@/schemas/client';
import { InboundOptionsSchema } from '@/schemas/client';
import { HttpUtil } from '@/utils';
import { formatInboundLabel } from '@/lib/inbounds/label';
import { parseMsg } from '@/utils/zodValidate';

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } } as const;

const MULTI_CLIENT_PROTOCOLS = new Set([
  'shadowsocks',
  'vless',
  'vmess',
  'trojan',
  'hysteria',
  'wireguard',
  'mtproto',
  'amneziawg',
  'tuic',
]);

interface GroupAccessModalProps {
  open: boolean;
  group: GroupSummary | null;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}

async function fetchInboundOptions(): Promise<InboundOption[]> {
  const msg = await HttpUtil.get('/panel/api/inbounds/options', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to load inbounds');
  const parsed = parseMsg(msg, InboundOptionsSchema, 'inbounds/options');
  return parsed.obj ?? [];
}

export default function GroupAccessModal({
  open,
  group,
  onOpenChange,
  onSaved,
}: GroupAccessModalProps) {
  const { t } = useTranslation();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [inbounds, setInbounds] = useState<InboundOption[]>([]);
  const [selected, setSelected] = useState<number[]>(() =>
    Array.isArray(group?.inboundIds) ? [...group.inboundIds] : [],
  );
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open || !group) return;
    let cancelled = false;
    void fetchInboundOptions()
      .then((rows) => {
        if (!cancelled) setInbounds(rows);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setInbounds([]);
          setError(err instanceof Error ? err.message : 'Failed to load inbounds');
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, group]);

  const options = useMemo(
    () =>
      inbounds
        .filter((ib) => MULTI_CLIENT_PROTOCOLS.has(ib.protocol || ''))
        .map((ib) => ({
          value: ib.id,
          label: formatInboundLabel(ib.tag, ib.remark),
          disabled: false,
        })),
    [inbounds],
  );

  async function save() {
    if (!group) return;
    setSaving(true);
    try {
      const msg = await HttpUtil.post(
        `/panel/api/clients/groups/${group.id}/access`,
        { inboundIds: [...new Set(selected)] },
        JSON_HEADERS,
      );
      if (!msg?.success) {
        messageApi.error(msg?.msg || t('somethingWentWrong'));
        return;
      }
      messageApi.success(
        t('pages.groups.accessSaved', { defaultValue: 'Доступ группы сохранён' }),
      );
      onSaved();
      onOpenChange(false);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      open={open}
      title={
        group
          ? t('pages.groups.accessTitle', {
              defaultValue: 'Доступ группы: {{name}}',
              name: group.name,
            })
          : ''
      }
      onCancel={() => onOpenChange(false)}
      width={620}
      destroyOnHidden
      footer={
        <Space>
          <Button onClick={() => onOpenChange(false)}>{t('cancel')}</Button>
          <Button type="primary" loading={saving} onClick={save}>
            {t('save')}
          </Button>
        </Space>
      }
    >
      {messageContextHolder}
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
          {t('pages.groups.accessDescription', {
            defaultValue:
              'Эта группа определяет, к каким входящим подключениям получают доступ её участники. Хосты и узлы наследуются через выбранные inbound.',
          })}
        </Typography.Paragraph>

        {error && <Alert type="error" showIcon message={error} />}

        <Typography.Text strong>
          {t('pages.groups.accessInbounds', { defaultValue: 'Доступные inbound' })}
        </Typography.Text>

        {loading ? (
          <div style={{ display: 'flex', justifyContent: 'center', padding: '24px 0' }}>
            <Spin />
          </div>
        ) : (
          <Select
            mode="multiple"
            style={{ width: '100%' }}
            value={selected}
            onChange={(values) => setSelected(values.map(Number))}
            options={options}
            placeholder={t('pages.groups.selectAccessInbounds', {
              defaultValue: 'Выберите inbound',
            })}
            maxTagCount="responsive"
            showSearch
            optionFilterProp="label"
            allowClear
          />
        )}

        <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
          {selected.length === 0
            ? t('pages.groups.noAccessWarning', {
                defaultValue: 'Пустой список означает: участники группы не получают доступа ни к одному inbound.',
              })
            : t('pages.groups.accessCount', {
                defaultValue: 'Выбрано inbound: {{count}}',
                count: selected.length,
              })}
        </Typography.Paragraph>
      </Space>
    </Modal>
  );
}
