<!-- togo-header -->
<div align="center">
  <img src=".github/assets/togo-mark.svg" alt="togo" height="64" />
  <h1>togo-framework/subscriptions</h1>
  <p>
    <a href="https://to-go.dev/marketplace"><img src="https://img.shields.io/badge/marketplace-to--go.dev-1FC7DC" alt="marketplace" /></a>
    <a href="https://pkg.go.dev/github.com/togo-framework/subscriptions"><img src="https://pkg.go.dev/badge/github.com/togo-framework/subscriptions.svg" alt="pkg.go.dev" /></a>
    <img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT" />
  </p>
  <p><strong>Part of the <a href="https://to-go.dev">togo</a> framework.</strong></p>
</div>

## Install

```bash
togo install togo-framework/subscriptions
```

<!-- /togo-header -->

<p align="center"><img src="https://to-go.dev/togo-mark.svg" width="72" alt="togo"></p>
<h1 align="center">togo · subscriptions</h1>

Subscription management for [togo](https://to-go.dev) — **plans, subscribe / cancel /
change, trials, and status** — built on the [`payment`](https://github.com/togo-framework/payment)
plugin. Charges are delegated to whatever `PaymentProvider` is registered in the
kernel; with no payment provider installed it still manages subscription state.

## Install

```bash
togo install togo-framework/subscriptions
# usually alongside a payment provider:
togo install togo-framework/payment-stripe
```

Tables (`plans`, `subscriptions`) are created on boot.

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/plans` | List plans |
| `POST` | `/api/plans` | Create a plan (`{name, price, currency, interval, features}`) |
| `GET` | `/api/subscriptions?user_id=` | A user's subscriptions |
| `POST` | `/api/subscriptions` | Subscribe (`{user_id, plan_id, trial_days}`) |
| `POST` | `/api/subscriptions/{id}/cancel` | Cancel |
| `POST` | `/api/subscriptions/{id}/change` | Upgrade/downgrade (`{plan_id}`) |

## Payment integration

The plugin looks up the `payment` service from the kernel container and, if it
satisfies the local `Charger` interface (`CreateSubscription`), creates a
provider-side subscription and stores the `provider_ref`. No hard import on
`payment`, so subscriptions builds and runs standalone.

MIT

<!-- togo-sponsors -->
---

<div align="center">
  <h3>Premium sponsors</h3>
  <p>
    <a href="https://id8media.com"><strong>ID8 Media</strong></a> &nbsp;·&nbsp;
    <a href="https://one-studio.co"><strong>One Studio</strong></a>
  </p>
  <p><sub>Support togo — <a href="https://github.com/sponsors/fadymondy">become a sponsor</a>.</sub></p>
</div>
<!-- /togo-sponsors -->
