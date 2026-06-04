# shop-operator

Kubernetes operator for the ShopHub platform (Phase F1 — skeleton).

Module: `github.com/shophub-platform/shop-operator` · Domain: `shophub.io` · Group/Version: `shop.shophub.io/v1alpha1`

## CRDs

| Kind | Short | Key spec fields |
|------|-------|-----------------|
| `Shop` | `shp` | `name`, `availability` (standard\|high), `walletRef`, `databaseType` (postgres\|redis), `image`, `replicas` (computed) |
| `DiscordChannel` | `dchan` | `guildID`, `channelName`, `notificationType`; status: `webhookURL`, `phase` |
| `Wallet` | `wlt` | `network` (sepolia\|mainnet), `address` (optional → generated); status: `address`, `encryptedPrivateKeyRef`, `phase` |

`availability` maps to replicas: `standard → 2`, `high → 3` (see `ReplicasForAvailability`).

## F1 scope

Reconcilers only **log** detected create/update/delete events; no child resources are created yet.

## Quickstart

```sh
make install                       # install CRDs into the current cluster
kubectl explain shop.spec          # all fields documented
make run                           # run the controller locally
kubectl apply -f config/samples    # operator logs "Reconciling shop ..."
make test                          # unit tests
make docker-build docker-push IMG=docker.io/shophub/shop-operator:0.1.0
```

## Note on generated code

`zz_generated.deepcopy.go` and `config/crd/bases/*.yaml` are normally produced by
`controller-gen` (`make generate` / `make manifests`). They are committed here so the
project builds without first running the generator. Re-run the generator after editing
any `*_types.go` to keep them in sync.
