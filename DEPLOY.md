# Deploying receipts-api to k3s

Everything here is run by Jack. Nothing in CI applies to the cluster; CI only builds and pushes
the image.

| What | Where |
|---|---|
| Image | `registry.k8s.blacktoaster.com/receipts/api:<sha7>` (built by `.github/workflows/build-api.yml` on push to `main`) |
| Chart | `./helm` (release `receipts`, namespace `receipts`) |
| Host | `receipts.k8s.blacktoaster.com` (VPN only; cert from ClusterIssuer `vault-issuer`) |
| Secrets | Vault KV `apps/receipts/app` -> ExternalSecret `receipts-app-secret` via ClusterSecretStore `vault-backend` |

## 0. Pre-flight checklist

Run these before the first install. Fix the values or the cluster before you continue.

1. **k3s is v1.29.4 or newer.** The probes and NetworkPolicy behavior in the chart were written
   for it.

   ```sh
   kubectl version -o json | jq -r .serverVersion.gitVersion      # e.g. v1.30.6+k3s1
   kubectl get nodes -o wide                                       # VERSION column, every node
   ```

2. **The ingress-nginx namespace and pod labels match `networkPolicy.ingressController`** in
   `helm/values.yaml`. The defaults are namespace `ingress-nginx` and label
   `app.kubernetes.io/name=ingress-nginx`. If they don't match, the NetworkPolicy blocks all
   traffic to the api.

   ```sh
   kubectl get ns ingress-nginx --show-labels        # needs kubernetes.io/metadata.name=ingress-nginx
   kubectl -n ingress-nginx get pods -l app.kubernetes.io/name=ingress-nginx --show-labels   # at least one pod
   kubectl get ingressclass nginx                    # ingress.className exists
   ```

   If the controller lives elsewhere, set
   `--set networkPolicy.ingressController.namespace=<ns>` and the matching `podLabels`.

3. **There is a default StorageClass.** The chart's PVCs (Postgres, uploads, backups) leave
   `storageClassName` empty.

   ```sh
   kubectl get storageclass     # one row marked "(default)", e.g. local-path on k3s
   ```

   Otherwise set `postgres.storage.className`, `api.uploads.className` and
   `backup.storage.className`.

4. **External Secrets Operator serves `external-secrets.io/v1beta1`.** The chart's
   ExternalSecret uses that version, and newer ESO releases may drop it.

   ```sh
   kubectl api-versions | grep '^external-secrets.io/'         # must list external-secrets.io/v1beta1
   kubectl get crd externalsecrets.external-secrets.io -o jsonpath='{.spec.versions[?(@.served==true)].name}'
   ```

5. **The cluster add-ons exist:**
   - `kubectl get clustersecretstore vault-backend` shows `Ready`.
   - `kubectl get clusterissuer vault-issuer` shows `Ready`.

## 1. Create the Vault secret (once)

Path: **`apps/receipts/app`** (same KV mount `vault-backend` reads for `apps/wc2026/app`).
All three properties are required; the ExternalSecret will not sync if one is missing.

| Property | Becomes | How to generate |
|---|---|---|
| `xai-api-key` | `XAI_API_KEY` in the api pod | your xAI console key (`xai-...`) |
| `mcp-token` | `MCP_TOKEN` (bearer for `POST /mcp`; at least 32 characters, the app refuses to start otherwise) | `openssl rand -hex 32` (64 hex characters) |
| `db-password` | `POSTGRES_PASSWORD` (postgres), `DB_PASSWORD` -> `DATABASE_URL` (api, migrate), `PGPASSWORD` (backup) | `openssl rand -hex 24` (hex only: it is embedded in a URL) |

Paste the values in your own terminal so they never land in shell history or a file:

```sh
read -rs XAI                     # paste the xAI key, Enter
vault kv put apps/receipts/app \
  xai-api-key="$XAI" \
  mcp-token="$(openssl rand -hex 32)" \
  db-password="$(openssl rand -hex 24)"
unset XAI
vault kv get -format=json apps/receipts/app | jq '.data.data | keys'   # ["db-password","mcp-token","xai-api-key"]
```

(Use `vault kv put -mount=<mount> ...` if your KV mount is not the default path prefix used by
`vault-backend`.) Read the generated `mcp-token` back for your MCP client with
`vault kv get -field=mcp-token apps/receipts/app`.

`db-password` is only read by Postgres `initdb` on the first start. Changing it later means also
running `ALTER ROLE receipts PASSWORD '...'` inside the database.

## 2. Get an image

Merge to `main` (or run the workflow manually: Actions -> build-api -> Run workflow). The job
summary prints the sha7. It only pushes after vet, unit and Postgres integration tests pass.

## 3. Deploy

```sh
TAG=<sha7>
helm lint ./helm
helm template receipts ./helm --set api.tag=$TAG | less     # review
helm upgrade --install receipts ./helm -n receipts --create-namespace --set api.tag=$TAG
```

Or bump `api.tag` in `helm/values.yaml` and commit it, like world-cup-site.

Order on first install: the ExternalSecret syncs `receipts-app-secret` (pods wait on it), Postgres
initdb runs with `--encoding=UTF8 --locale=en_US.UTF-8`, then the api pod's `migrate` init
container retries until Postgres is up and applies all migrations, then the api starts.

## 4. Verify

```sh
kubectl -n receipts get externalsecret,secret,pods,pvc,cronjob,certificate,ingress
kubectl -n receipts get externalsecret receipts-app-secret -o jsonpath='{.status.conditions[0].reason}'  # SecretSynced
kubectl -n receipts logs deploy/receipts-api -c migrate
kubectl -n receipts exec deploy/receipts-api -- wget -qO- localhost:8080/readyz   # ok (DB reachable)
kubectl -n receipts logs deploy/receipts-api | head -30     # "image decode budget", "HEIC uploads enabled"
kubectl -n receipts exec statefulset/receipts-postgres -- \
  psql -U receipts -d receipts -Atc "select datcollate, pg_encoding_to_char(encoding) from pg_database where datname='receipts'"
# expect: en_US.UTF-8|UTF8
```

## 5. Smoke test

```sh
H=https://receipts.k8s.blacktoaster.com
curl -fsS $H/api/v1/health

# Upload one receipt photo. NOTE: this makes one live grok extraction call with the key from Vault.
# Do not use testdata/golden-local photos for this; use a fresh receipt.
curl -fsS -F image=@/path/to/receipt.jpg $H/api/v1/uploads | jq .      # 202, note receipt_id
curl -fsS $H/api/v1/receipts/<receipt_id> | jq .   # status: uploaded -> parsing -> parsed | needs_review

# MCP: list the tools (MCP_TOKEN = the mcp-token from Vault)
MCP_TOKEN=$(vault kv get -field=mcp-token apps/receipts/app)
curl -fsS $H/mcp -H "Authorization: Bearer $MCP_TOKEN" -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq '.result.tools[].name'
curl -s -o /dev/null -w '%{http_code}\n' $H/mcp -d '{}'     # 401 without the token

# Web UI
open $H/
```

Backups: run one now and check it landed.

```sh
kubectl -n receipts create job --from=cronjob/receipts-pg-backup receipts-pg-backup-manual
kubectl -n receipts wait --for=condition=complete job/receipts-pg-backup-manual --timeout=15m \
  || { echo "backup did not complete"; kubectl -n receipts describe job/receipts-pg-backup-manual; }
kubectl -n receipts logs job/receipts-pg-backup-manual      # "wrote /backups/receipts-<ts>.dump"
kubectl -n receipts delete job receipts-pg-backup-manual    # so the name can be reused
```

## 6. Roll back

**Migrations are not all additive.** Migration 014 drops `receipts.store_category`,
`store_address` and `store_phone` and renames `store_name` to `printed_merchant_name`. 015
normalizes blank merchant and store fields to NULL. An image from before 014 running on a
014+ database fails every receipt read with 500, while `/api/v1/health` stays 200. Images from
this PR on also gate `/readyz` on the schema: when the applied migrations differ from the ones in
the image (missing or unknown versions), the pod is not ready and the response lists the
versions.

### Image-only rollback (target image has the same migrations)

```sh
helm -n receipts history receipts
helm -n receipts rollback receipts <REVISION>
kubectl -n receipts rollout status deploy/receipts-api   # readiness confirms the schema matches
```

### Rollback across migrations (for example to an image from before 014)

Order matters: **first migrate down with the NEW (current) image, then roll the image back.**
Only the new image has the down files. The old image doesn't know the newer versions, so it
can't undo them. It also doesn't migrate down at startup: it would just run against the newer
schema.

1. Take a manual backup (section 5) and keep it. It is the only way back to the exact current data.
2. Stop the app so nothing writes during the change:

   ```sh
   kubectl -n receipts scale deploy/receipts-api --replicas=0
   ```

3. Migrate down with the current image. N = the number of migrations newer than the target image.
   To go back to a pre-014 image, N=2 (015, then 014).

   ```sh
   # app=receipts-migrate: allowed to reach Postgres by the NetworkPolicy, but NOT selected by the
   # receipts-api Service, so the ingress never routes traffic to this pod
   kubectl -n receipts run migrate-down --rm -it --restart=Never --labels=app=receipts-migrate \
     --image=registry.k8s.blacktoaster.com/receipts/api:<current-sha7> \
     --overrides='{"spec":{"containers":[{"name":"m","image":"registry.k8s.blacktoaster.com/receipts/api:<current-sha7>","command":["./migrate","down","-steps","N"],"env":[{"name":"DB_PASSWORD","valueFrom":{"secretKeyRef":{"name":"receipts-app-secret","key":"db-password"}}},{"name":"DATABASE_URL","value":"postgres://receipts:$(DB_PASSWORD)@receipts-postgres:5432/receipts?sslmode=disable"}]}]}}'
   ```

4. Roll the image back and scale up:

   ```sh
   helm -n receipts rollback receipts <REVISION>
   kubectl -n receipts scale deploy/receipts-api --replicas=1
   ```

   The rollback restores the old replica count. The old image's startup migrate is a no-op.

**What `014` down loses (lossy):**

- The legacy columns come back, filled from the normalized tables, not from the original values:
  - each receipt gets its merchant's category and its store's address and phone;
  - where a receipt's own pre-014 value differed from its merchant's or store's, the original is
    gone.
- Receipts without a merchant get a NULL category. Receipts without a store get a NULL address
  and phone.
- Phones on receipts that had no store row were already dropped by 014 up. Down can't bring them
  back.
- The blank-vs-NULL distinction in the old columns is lost.
- 014 up's writes to `merchants.category` and `stores.address`/`phone` stay; down doesn't undo
  them.

`015` down is exact: it restores the blank values it replaced with NULL and drops its CHECKs.

The only full recovery from these losses is restoring the pre-upgrade backup (below).

## Restore from a backup

```sh
# pause the nightly backup so it cannot dump a half-restored database
kubectl -n receipts patch cronjob receipts-pg-backup -p '{"spec":{"suspend":true}}'
# Wait until no backup job is running: scheduled ones and manual `create job --from` ones
# (all named receipts-pg-backup-*). It fails closed: a kubectl error or the 30-minute timeout
# stops here (non-zero exit), and you must not go on with the restore until it passes.
(
  deadline=$((SECONDS + 1800))
  while :; do
    out=$(kubectl -n receipts get jobs -o jsonpath='{range .items[?(@.status.active)]}{.metadata.name}{"\n"}{end}') || { echo "kubectl failed; NOT safe to restore"; exit 1; }
    active=$(printf '%s\n' "$out" | grep '^receipts-pg-backup' || true)
    [ -z "$active" ] && { echo "no backup running"; exit 0; }
    [ "$SECONDS" -ge "$deadline" ] && { echo "timed out waiting for: $active"; exit 1; }
    echo "waiting for: $active"; sleep 5
  done
) || echo "STOP: do not restore yet"
kubectl -n receipts scale deploy/receipts-api --replicas=0
# the app=receipts-backup label is what the NetworkPolicy lets reach Postgres
kubectl -n receipts run pg-restore --rm -it --restart=Never --labels=app=receipts-backup --image=postgres:16-bookworm \
  --overrides='{"spec":{"securityContext":{"runAsNonRoot":true,"runAsUser":999,"runAsGroup":999,"fsGroup":999},"volumes":[{"name":"b","persistentVolumeClaim":{"claimName":"receipts-backups"}}],"containers":[{"name":"r","image":"postgres:16-bookworm","stdin":true,"tty":true,"command":["bash"],"volumeMounts":[{"name":"b","mountPath":"/backups"}],"env":[{"name":"PGHOST","value":"receipts-postgres"},{"name":"PGUSER","value":"receipts"},{"name":"PGPASSWORD","valueFrom":{"secretKeyRef":{"name":"receipts-app-secret","key":"db-password"}}}]}]}}'
# inside the pod:
ls -1t /backups
pg_restore --clean --if-exists --no-owner -d receipts /backups/receipts-<ts>.dump
exit
kubectl -n receipts scale deploy/receipts-api --replicas=1
kubectl -n receipts patch cronjob receipts-pg-backup -p '{"spec":{"suspend":false}}'   # resume backups
kubectl -n receipts get cronjob receipts-pg-backup -o jsonpath='{.spec.suspend}'        # false
```

Do a restore drill into a scratch database (`createdb receipts_drill; pg_restore -d receipts_drill ...`)
before trusting the backups.

Receipt photos live on the `receipts-uploads` PVC and are not in the pg_dump. Back that volume up
with the cluster's volume snapshots (or copy `/app/data/uploads`) until the object store lands.
Both PVCs (`receipts-uploads`, `receipts-backups`) carry `helm.sh/resource-policy: keep`, so
`helm uninstall` leaves them; the Postgres PVC comes from the StatefulSet and is also kept.

## Network policy

The chart installs ingress-only NetworkPolicies. All ingress in the namespace is denied by
default. The allowed paths are:

- ingress-nginx to api:8080
- api pods (including the `migrate` init container) to postgres:5432
- `app=receipts-backup` pods to postgres:5432
- `app=receipts-migrate` one-off pods (migrate-down) to postgres:5432

Egress is not restricted: the api needs DNS and HTTPS to `api.x.ai`.

**Assumption:** the ingress controller runs in namespace `ingress-nginx` with label
`app.kubernetes.io/name=ingress-nginx`. world-cup-site has no NetworkPolicy to copy. Check it:

```sh
kubectl get pods -A -l app.kubernetes.io/name=ingress-nginx --show-labels
```

If it differs, set `networkPolicy.ingressController.*`, or set `networkPolicy.enabled=false`.
Any ad-hoc pod that needs Postgres must carry `app=receipts-backup` or `app=receipts-migrate`.
Don't use `app=receipts-api`: the api Service would route traffic to that pod.
The restore and migrate-down commands above already do.

## Memory (OPS-07)

The api pod requests 512Mi and is limited to 1536Mi with `GOMEMLIMIT=1200MiB`. Decoded image
bitmaps share a global budget (`IMAGE_DECODE_BUDGET_BYTES`, default 200 MiB), so only one
maximum-size image (for example 7000x7000) decodes at a time, and smaller photos still decode two
at a time. If you raise the budget, raise `api.resources.limits.memory` too: roughly 1 GiB for
each maximum-size decode in flight.
