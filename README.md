# shop-operator

This is the Kubernetes operator for the ShopHub platform, written in Go with kubebuilder and controller-runtime. It is the "executor" part of the platform: the ShopHub back end never creates Kubernetes resources directly. Instead it creates, updates, and deletes custom resources (`Shop`, `DiscordChannel`, `Wallet`), and this operator watches those resources through a reconcile loop and provisions everything a shop needs, including Deployments, Services, Ingress, a database, monitoring, Discord notifications, and a blockchain wallet.

Module: `github.com/shophub-platform/shop-operator`. Domain: `shophub.io`. Group and version: `shop.shophub.io/v1alpha1`.

## Role in the architecture

The operator is the bridge between ShopHub's API layer and the actual state of the cluster. When a user creates a shop in ShopHub, the back end writes a `Shop` custom resource. This operator notices it and creates all the supporting resources: a database through the CNPG or Redis operator, Deployments for the shop's front end and back end, a Service, an Ingress, a ConfigMap and Secret with configuration, a ServiceMonitor and PodMonitor for Prometheus, and a Grafana dashboard. It then writes the provisioning status back into `Shop.status.phase`, which ShopHub reads and shows to the user. The `DiscordChannel` custom resource manages a notification channel, and the `Wallet` custom resource manages the blockchain account that a shop uses to receive payments.

## Custom resource definitions

`Shop` (short name `shp`) has spec fields for name, availability (standard or high), a wallet reference, database type (postgres or redis), image, and a computed replica count. `DiscordChannel` (short name `dchan`) has spec fields for guild ID, channel name, and notification type, with status fields for the webhook URL and phase. `Wallet` (short name `wlt`) has spec fields for network (sepolia or mainnet) and an optional address (generated automatically if omitted), with status fields for address, an encrypted private key reference, and phase. Availability maps to replica count through `ReplicasForAvailability`: standard maps to 2 replicas, high maps to 3.

## Implementation status

The Shop reconciler logic, covering all 13 steps described below, is implemented. The DiscordChannel reconciler, using the Discord REST API for channel and webhook creation plus a finalizer for cleanup, is implemented. The Wallet reconciler, covering address validation and key pair generation with AES-GCM encryption plus a finalizer, is implemented. Operator tests, including unit and chaos tests with coverage tracking, are in place: unit tests using a fake client exist for all three reconcilers, along with a unit level chaos test, and coverage sits at or above 70 percent; Kind based integration testing is documented separately. The Definition of Done, which requires an end to end run under 60 seconds, resource cleanup, and coverage of at least 70 percent, is still pending completion of the items above.

### Shop reconciler

`internal/controller/shop_controller.go`, together with `shop_resources.go` and `shop_external.go`, implements all 13 steps from the specification. It receives the Shop custom resource and exits if it no longer exists, since children are cleaned up through owner reference garbage collection. It validates the spec, requiring that the referenced Wallet exists and that databaseType is either postgres or redis. It reconciles the DiscordChannel, creating the custom resource and reading the webhook URL once it is ready. It reconciles the database, creating a CNPG Cluster for postgres or a Redis custom resource for redis, and blocks until it is ready. It creates a ConfigMap with application configuration such as DB_HOST, DB_PORT, and WALLET_ADDRESS. It creates a Secret containing DATABASE_URL and DISCORD_WEBHOOK_URL. It creates a Deployment for the shop back end, with replicas set to 2 for standard or 3 for high, sourcing environment variables from the ConfigMap and Secret. It creates a Deployment for the shop front end. It creates ClusterIP Services and an Ingress that routes the root path to the front end and `/api` to the back end, at `<name>.shophub.local`. It creates a ServiceMonitor and PodMonitor for Prometheus. It creates a Grafana dashboard ConfigMap labeled `grafana_dashboard: "1"`. It sets an owner reference on every resource it creates, enabling cascading garbage collection. Finally, it updates status fields including Phase, ObservedGeneration, Conditions, URL, ReplicaCount, and ReadyReplicas.

Two design decisions were made here. `Spec.Image` refers to the back end image, while the front end image is supplied through the annotation `shop.shophub.io/frontend-image`, defaulting to a placeholder until a real front end image exists. Because the Discord and Wallet reconcilers were completed later, Discord integration is treated as best effort: a channel is created and its webhook used once ready, and Wallet only needs to exist. A strict gate that blocks provisioning until Discord is ready, as described in the specification, can be enabled with `REQUIRE_DISCORD=true`. External database and monitoring operators are treated strictly.

### DiscordChannel reconciler

`internal/controller/discordchannel_controller.go` and `internal/discord/client.go` implement a Discord REST API client under `internal/discord` that creates a text channel with `POST /guilds/{id}/channels`, creates a webhook with `POST /channels/{id}/webhooks`, and deletes a channel with `DELETE /channels/{id}`, against the base URL `https://discord.com/api/v10`, overridable through `DISCORD_API_BASE`. The bot token is read from a Secret named `discord-bot-token` in the operator's namespace, resolved through the `OPERATOR_NAMESPACE` or `POD_NAMESPACE` environment variable, then the service account namespace file, then falling back to `default`; supported keys are `token`, `bot-token`, and `DISCORD_BOT_TOKEN`. The reconciler creates the channel, then the webhook, and stores the webhook URL in a Secret named `discord-webhook-<name>`. A finalizer named `shop.shophub.io/discordchannel-cleanup` deletes the Discord channel and the webhook Secret when the custom resource is deleted. Status fields track Phase (Pending, Creating, Ready, or Failed), ChannelID, WebhookID, WebhookURL, and Conditions, and the reconciler is idempotent, skipping creation if the IDs are already present in status.

### Wallet reconciler

`internal/controller/wallet_controller.go` and `internal/wallet/wallet.go` handle two cases. If `Spec.Address` is set, the reconciler validates its format using EIP-55 through `go-ethereum/common`, stores the normalized address in `Status.Address`, and sets `Phase=Ready`, without creating a Secret since the key is external. If `Spec.Address` is empty, the reconciler generates a new secp256k1 key pair using `go-ethereum/crypto`, encrypts the private key with AES-256-GCM, and stores it in a Secret named `<name>-wallet-key` under the key `encrypted-private-key`, with `Status.EncryptedPrivateKeyRef` pointing to it. The AES passphrase is read from the environment variable `WALLET_ENCRYPTION_KEY` or from a Secret named `wallet-encryption-key` (key `key` or `passphrase`) in the operator's namespace, and a 32 byte AES key is derived from it with SHA-256. A finalizer named `shop.shophub.io/wallet-cleanup` deletes the Secret when the Wallet custom resource is deleted, and the reconciler is idempotent, doing nothing once `Status.Phase=Ready`.

## Running and testing

On Windows and PowerShell, `make` is not available, so `go` and `kubectl` are used directly. On Linux and macOS, `make build`, `make test`, `make install`, and `make run` still work.

To check that the project compiles without needing a cluster:

```powershell
cd D:\FAKULTET\MASTER\DEVOPS\shop-operator
go build ./...
go vet ./...
go test ./...        # existing unit tests (api package)
```

Running the strict mode reconciler against a real cluster requires several external operators to already be installed: CNPG (CloudNativePG) for postgres databases, the opstree Redis operator (`redis.redis.opstreelabs.in/v1beta2`) for redis databases, the kube-prometheus-stack CRDs for ServiceMonitor and PodMonitor, and an nginx ingress controller.

To run locally:

```powershell
kind create cluster

# install the CRDs (replaces `make install`)
kubectl apply -f config/crd/bases

# run the operator locally (replaces `make run`)
go run ./cmd/main.go
```

In a second terminal, apply the Wallet sample first since Shop depends on it, then the Shop sample, and watch the phase transition from Pending to Provisioning to Ready:

```powershell
kubectl apply -f config/samples/shop_v1alpha1_wallet.yaml
kubectl apply -f config/samples/shop_v1alpha1_shop.yaml

kubectl get shops -w
kubectl describe shop <name>
kubectl get deploy,svc,ingress,configmap,secret -l app.kubernetes.io/managed-by=shop-operator
```

Testing the DiscordChannel reconciler requires a real Discord bot token from the Developer Portal and the guild ID of a server where the bot can create channels. Tell the operator which namespace holds the token, then create the DiscordChannel custom resource with the guild ID filled in:

```powershell
$env:OPERATOR_NAMESPACE="default"
kubectl create secret generic discord-bot-token --from-literal=token=<BOT_TOKEN> -n default
go run ./cmd/main.go
```

```powershell
kubectl apply -f config/samples/shop_v1alpha1_discordchannel.yaml
kubectl get dchan -w
kubectl get secret discord-webhook-<name>
kubectl delete dchan <name>
```

Without a real token or cluster, the logic can also be tested with envtest against a mock Discord server by pointing `DISCORD_API_BASE` at a local test HTTP server.

Testing the Wallet reconciler requires an AES passphrase, most easily supplied through the environment:

```powershell
$env:OPERATOR_NAMESPACE="default"
$env:WALLET_ENCRYPTION_KEY="a secret passphrase"
go run ./cmd/main.go
```

To generate a new key pair, leave `Spec.Address` empty and watch the phase move to Ready with the address populated:

```powershell
kubectl apply -f config/samples/shop_v1alpha1_wallet.yaml
kubectl get wlt -w
kubectl get wlt <name> -o jsonpath="{.status}"
kubectl get secret <name>-wallet-key
kubectl delete wlt <name>
```

To validate an existing address instead, set `spec.address` in the sample to a valid 0x prefixed 40 character hex address; the operator only checks and normalizes it into `Status.Address` without creating a Secret. An invalid address results in `Phase=Failed`.

All reconciler unit tests use a fake client and run without a real cluster or envtest:

```powershell
go test ./... -cover
```

Coverage across the internal packages, against a target of at least 70 percent, can be checked with:

```powershell
go test -coverpkg=./internal/... -coverprofile=cover.out ./...
go tool cover -func=cover.out | Select-String "total:"
go tool cover -html=cover.out
```

Current coverage is roughly 82 percent for `internal/wallet`, 82 percent for `internal/discord`, and 70 percent for `internal/controller`, including all three reconcilers, builders, and a unit level chaos test that deletes a Deployment and checks that the operator recreates it. A Kind based integration test, applying a Shop resource and validating that it reaches Ready with all resources present, along with a live chaos test, require the full operator stack (CNPG, Redis, Prometheus) and are run manually against a Kind cluster.

Building the Docker image:

```powershell
docker build --build-arg VERSION=v0.1.0 -t docker.io/shophub/shop-operator:0.1.0 .
```

## Note on generated code

`zz_generated.deepcopy.go` and the files under `config/crd/bases/*.yaml` are normally produced by `controller-gen` through `make generate` and `make manifests`. They are committed here so the project builds without first running the generator. Re-run the generator after editing any `*_types.go` file to keep them in sync.

## Technical stack

The operator is written in Go using the kubebuilder v4 project layout and controller-runtime for the reconcile loop, manager, and client, with `zz_generated.deepcopy.go` and the CRD YAML generated through controller-gen. External integrations include the Discord REST API for creating channels and webhooks through a bot token, and go-ethereum (`common`, `crypto`) for validating and generating Ethereum addresses and keys, with AES-256-GCM encryption of the private key stored in a Kubernetes Secret. The operator relies on several external operators: CNPG (CloudNativePG) for PostgreSQL databases, the opstree Redis operator for Redis databases, the kube-prometheus-stack CRDs (ServiceMonitor and PodMonitor) for monitoring, and an nginx ingress controller. Testing includes unit tests with a fake client that run without a cluster, Kind and envtest based integration tests, and a coverage gate of at least 70 percent. For build and deployment, a Dockerfile builds the operator image and a Makefile follows the kubebuilder standard; installation into a cluster happens through the `shop-operator` Helm chart in the `helm-charts` repository, which also includes the CRDs.
