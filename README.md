# shop-operator

Kubernetes operator for the ShopHub platform (Faza F5 — Shop + DiscordChannel reconciler-i implementirani; Wallet reconciler u toku).

Module: `github.com/shophub-platform/shop-operator` · Domain: `shophub.io` · Group/Version: `shop.shophub.io/v1alpha1`

## CRDs

| Kind | Short | Key spec fields |
|------|-------|-----------------|
| `Shop` | `shp` | `name`, `availability` (standard\|high), `walletRef`, `databaseType` (postgres\|redis), `image`, `replicas` (computed) |
| `DiscordChannel` | `dchan` | `guildID`, `channelName`, `notificationType`; status: `webhookURL`, `phase` |
| `Wallet` | `wlt` | `network` (sepolia\|mainnet), `address` (optional → generated); status: `address`, `encryptedPrivateKeyRef`, `phase` |

`availability` maps to replicas: `standard → 2`, `high → 3` (see `ReplicasForAvailability`).

## Faza F5 — status implementacije

| Stavka | Opis | Status |
|--------|------|--------|
| **10.1** | Shop reconciler logika (13 koraka) | ✅ Implementirano |
| **10.2** | DiscordChannel reconciler (Discord REST API, webhook, finalizer) | ✅ Implementirano |
| **10.3** | Wallet reconciler (validacija adrese / key-pair generisanje, AES-GCM, finalizer) | ⏳ Skeleton (samo loguje) |
| **10.4** | Testovi operatora (envtest unit, kind integracioni, chaos) | ⏳ Nije započeto |
| **10.5** | Definition of Done (E2E < 60s, cleanup, coverage ≥ 70%) | ⏳ Čeka 10.2–10.4 |

### 10.1 Shop reconciler — šta je urađeno

`internal/controller/shop_controller.go` (+ `shop_resources.go`, `shop_external.go`)
implementira svih 13 koraka iz specifikacije:

1. Prima Shop CR; ako ne postoji → exit (children se brišu preko owner-reference GC).
2. Validira Spec: referencirani **Wallet** mora postojati; **databaseType** mora biti `postgres` ili `redis`.
3. Reconcile **DiscordChannel** (kreira CR; čita webhook URL kad je Ready).
4. Reconcile **baze**: kreira CNPG `Cluster` (postgres) ili `Redis` CR (redis); blokira dok nije Ready.
5. **ConfigMap** sa konfiguracijom aplikacije (`DB_HOST`, `DB_PORT`, `WALLET_ADDRESS`, …).
6. **Secret** sa `DATABASE_URL` i `DISCORD_WEBHOOK_URL`.
7. **Deployment** za Shop BE: `replicas = standard?2:3`, `envFrom` ConfigMap + Secret.
8. **Deployment** za Shop FE.
9. **Service**-ovi (ClusterIP) + **Ingress** (`<name>.shophub.local`, `/`→FE, `/api`→BE).
10. **ServiceMonitor** + **PodMonitor** (Prometheus).
11. **Grafana** dashboard ConfigMap (label `grafana_dashboard: "1"`).
12. **OwnerReference** na sve kreirane resurse (cascading garbage collection).
13. **Status**: `Phase`, `ObservedGeneration`, `Conditions`, `URL`, `ReplicaCount`, `ReadyReplicas`.

Dvije projektne odluke u 10.1:

- `Spec.Image` je **backend** image; frontend image se zadaje anotacijom
  `shop.shophub.io/frontend-image` (default placeholder dok FE image ne postoji).
- Pošto 10.2/10.3 još nisu gotovi, **Discord** je *best-effort* (kreira kanal, koristi
  webhook kad bude Ready), a **Wallet** mora samo *postojati*. Strogi gate iz
  specifikacije ("blokiraj dok Discord nije Ready") se uključuje sa
  `REQUIRE_DISCORD=true`. Eksterni operatori za bazu/monitoring se tretiraju striktno.

### 10.2 DiscordChannel reconciler — šta je urađeno

`internal/controller/discordchannel_controller.go` + `internal/discord/client.go`:

- **Discord REST API** klijent (`internal/discord`): `POST /guilds/{id}/channels` (tekstualni
  kanal), `POST /channels/{id}/webhooks` (webhook), `DELETE /channels/{id}` (cleanup).
  Bazni URL je `https://discord.com/api/v10`, override preko `DISCORD_API_BASE`.
- **Bot token** se čita iz Secreta `discord-bot-token` u namespace-u operatora
  (`OPERATOR_NAMESPACE`/`POD_NAMESPACE` env, pa service-account namespace fajl, pa `default`).
  Podržani ključevi: `token`, `bot-token`, `DISCORD_BOT_TOKEN`.
- Kreira kanal → kreira webhook → **webhook URL čuva u Secretu `discord-webhook-<name>`**.
- **Finalizer** `shop.shophub.io/discordchannel-cleanup`: pri brisanju CR-a briše Discord
  kanal i webhook Secret.
- Status: `Phase` (Pending→Creating→Ready/Failed), `ChannelID`, `WebhookID`, `WebhookURL`,
  `Conditions`. Idempotentno (ne kreira ponovo ako su ID-evi već u statusu).

## Pokretanje i testiranje

> Na **Windows / PowerShell** `make` ne postoji — koriste se `go` i `kubectl` direktno.
> (Na Linux/macOS i dalje rade `make build`, `make test`, `make install`, `make run`.)

### 1. Provjera kompilacije (ne treba klaster)

```powershell
cd D:\FAKULTET\MASTER\DEVOPS\shop-operator
go build ./...
go vet ./...
go test ./...        # postojeći unit testovi (api paket)
```

### 2. Preduslovi na klasteru (striktni mod 10.1)

Reconciler kreira resurse iz eksternih operatora, pa moraju biti instalirani:

- **CNPG** (CloudNativePG) — za `postgres` baze
- **Redis operator** (opstree `redis.redis.opstreelabs.in/v1beta2`) — za `redis` baze
- **kube-prometheus-stack** — CRD-ovi `ServiceMonitor` / `PodMonitor`
- **Ingress controller** (nginx)

### 3. Lokalno pokretanje

```powershell
kind create cluster

# instaliraj CRD-ove (zamjena za `make install`)
kubectl apply -f config/crd/bases

# pokreni operatora lokalno (zamjena za `make run`)
go run ./cmd/main.go
```

U drugom prozoru:

```powershell
# prvo Wallet (Shop zavisi od njega), pa Shop
kubectl apply -f config/samples/shop_v1alpha1_wallet.yaml
kubectl apply -f config/samples/shop_v1alpha1_shop.yaml

kubectl get shops -w          # prati Phase: Pending -> Provisioning -> Ready
kubectl describe shop <ime>   # Conditions, URL, ReplicaCount
kubectl get deploy,svc,ingress,configmap,secret -l app.kubernetes.io/managed-by=shop-operator
```

### 3b. Test DiscordChannel reconciler-a (10.2)

Treba ti pravi Discord bot token (Developer Portal → Bot) i Guild ID servera u koji
bot ima dozvolu da kreira kanale. Operatoru reci u kom je namespace-u token:

```powershell
$env:OPERATOR_NAMESPACE="default"
kubectl create secret generic discord-bot-token --from-literal=token=<BOT_TOKEN> -n default
go run ./cmd/main.go
```

U drugom prozoru kreiraj DiscordChannel CR (popuni `guildID` u sample-u):

```powershell
kubectl apply -f config/samples/shop_v1alpha1_discordchannel.yaml
kubectl get dchan -w                       # Phase Pending -> Creating -> Ready
kubectl get secret discord-webhook-<ime>   # webhook URL je tu, ključ "url"
kubectl delete dchan <ime>                  # finalizer briše kanal + webhook secret
```

> Bez pravog tokena/klastera možeš logiku testirati i envtest-om sa mock Discord serverom
> (`DISCORD_API_BASE` → tvoj test HTTP server) — to je dio 10.4.

### 4. Docker image

```powershell
docker build --build-arg VERSION=v0.1.0 -t docker.io/shophub/shop-operator:0.1.0 .
```

## Note on generated code

## Note on generated code

`zz_generated.deepcopy.go` and `config/crd/bases/*.yaml` are normally produced by
`controller-gen` (`make generate` / `make manifests`). They are committed here so the
project builds without first running the generator. Re-run the generator after editing
any `*_types.go` to keep them in sync.
