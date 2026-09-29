# Project instructions

- This repository specifies a Go public-market analysis engine. Do not add account authentication, private APIs, wallets, order placement, execution, or real-position management.
- Read `docs/SPEC.md` for authoritative requirements, `docs/ARCHITECTURE.md` for contracts and ownership, `docs/RESEARCH.md` for hypotheses, `docs/PLAN.md` for milestone gates, and `docs/STATUS.md` before changing scope.
- Keep live and replay on the same normalization, quality, feature, signal, and decision path. An analysis at logical time T may use only inputs available by T.
- Preserve raw arrival order, source provenance, time fields, and explicit gaps. Bad or missing required data makes the affected analysis unavailable and generally yields `NO_TRADE`.
- Treat model performance as unproven until chronological, cost-aware, leakage-resistant validation supports it. Do not report hypotheses as results.
- Update the relevant docs and `docs/STATUS.md` when an architectural decision changes. Implement only the active milestone in `docs/PLAN.md` unless the user changes scope.
- Branch by milestone: `main` holds the latest accepted milestone; develop and validate each milestone on its own branch (`v0`, then `v1`, `v2`, and so on). Commit milestone work to its branch, not directly to `main`. After acceptance, verify a clean branch, merge it into `main`, create and push an annotated completion tag, then create the next milestone branch when that work begins. Preserve history; do not force-push or rebase shared commits. Record the exact clean commit used for each acceptance soak in its run report and manifest. V0 was accepted from source commit `d26241151331795cf5005ba09d5f176d1efb6b22`; do not treat that as evidence of predictive value.
