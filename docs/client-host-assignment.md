# Client → Host Assignment

## Goal

Add per-client Host assignment without replacing the existing client → inbound machinery.

## Current upstream model

- `clients` stores the logical client identity.
- `client_inbounds` stores client ↔ inbound membership.
- `hosts` stores Host rows keyed to an `inbound_id`.
- Subscription generation currently loads all enabled Hosts for an inbound and renders them for
  every client attached to that inbound.

## Target model

Add a durable logical assignment:

`client_hosts(client_id, group_id)`

The assignment targets `HostGroup.group_id`, not the physical Host row id. A Host group may contain
multiple Host rows and/or multiple inbounds; assigning the group therefore survives HostGroup edits
that recreate its rows.

Assignment state is defined entirely by the relation table:

- no `client_hosts` rows → legacy mode, using all enabled Hosts of the client's inbound;
- one or more `client_hosts` rows → restricted mode, using only those HostGroups;
- restricted mode with no matching Host on an inbound → no link for that inbound, with no legacy
  fallback.

`client_inbounds` remains the runtime/Xray attachment layer and is never changed implicitly by Host
assignment.

## Compatibility

Existing clients are kept in legacy mode automatically because they have no `client_hosts` rows.

Clearing all Host assignments returns the client to legacy Host behavior.

## Subscription integration

Do not rewrite protocol-specific link generators. Keep the existing Host endpoint projection and
replace only the Host selection boundary with a client-aware resolver:

`hostEndpointsForClient(inbound, format, email)`

It must distinguish:

- legacy client: all enabled applicable Hosts for the inbound;
- Host-routing client: only enabled Hosts whose group is assigned to that client;
- Host-routing client with no matching Host on this inbound: no link for that inbound.

## Safety rules

The patch must fail closed when an expected integration point is absent. It must never silently
apply a partially recognized patch after an upstream architectural change.

## Runtime packaging

The frontend is not deployed as a separate runtime component. The React SPA is built into
`internal/web/dist` and embedded into the Go `x-ui` binary at build time.

For an existing installation, the feature can therefore be deployed as a drop-in replacement of the
panel binary while preserving the existing database, service unit, subscription settings, Xray
binaries, and other installation data.

Do not treat this as a DLL/plugin injection. 3x-ui does not expose a plugin-loading boundary for
this feature. External response filtering is possible only as a separate subscription proxy/service,
which would duplicate part of 3x-ui's subscription logic and create another compatibility surface.

## Upstream maintenance

The `client-host` branch must remain the only patched branch; `main` stays aligned with
`MHSanaei/3x-ui`.

The repository includes `.github/workflows/upstream-sync.yml`, which performs a scheduled
clean-merge check against upstream `main` and opens an upstream-sync PR when the merge applies
cleanly. If Git cannot merge the upstream changes without conflicts, the workflow fails closed and
does not modify `client-host`.

This makes upstream changes detectable before deployment. A future upstream change in the client,
Host, subscription, or generated-API integration points may still require a manual patch adjustment.
