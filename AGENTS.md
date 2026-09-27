# Project instructions

- This repository specifies a Go public-market analysis engine. Do not add account authentication, private APIs, wallets, order placement, execution, or real-position management.
- Read `docs/SPEC.md` for authoritative requirements, `docs/ARCHITECTURE.md` for contracts and ownership, `docs/RESEARCH.md` for hypotheses, `docs/PLAN.md` for milestone gates, and `docs/STATUS.md` before changing scope.
- Keep live and replay on the same normalization, quality, feature, signal, and decision path. An analysis at logical time T may use only inputs available by T.
- Preserve raw arrival order, source provenance, time fields, and explicit gaps. Bad or missing required data makes the affected analysis unavailable and generally yields `NO_TRADE`.
- Treat model performance as unproven until chronological, cost-aware, leakage-resistant validation supports it. Do not report hypotheses as results.
- Update the relevant docs and `docs/STATUS.md` when an architectural decision changes. Implement only the active milestone in `docs/PLAN.md` unless the user changes scope.
- Branch by milestone: `main` holds the latest accepted milestone; develop and validate the active milestone on `v0`, then `v1`, `v2`, and so on. V0 is not accepted yet, so commit all V0 fixes and soak evidence changes to `v0`, never directly to `main`. Do not create `v1` until V0 passes its full gate, `v0` is clean, `v0` is merged into `main`, and an annotated `v0-complete` tag is pushed. Preserve history; do not force-push or rebase shared commits. Record the exact clean commit used for each acceptance soak in the run report and manifest.
