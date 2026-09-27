# Project instructions

- This repository specifies a Go public-market analysis engine. Do not add account authentication, private APIs, wallets, order placement, execution, or real-position management.
- Read `docs/SPEC.md` for authoritative requirements, `docs/ARCHITECTURE.md` for contracts and ownership, `docs/RESEARCH.md` for hypotheses, `docs/PLAN.md` for milestone gates, and `docs/STATUS.md` before changing scope.
- Keep live and replay on the same normalization, quality, feature, signal, and decision path. An analysis at logical time T may use only inputs available by T.
- Preserve raw arrival order, source provenance, time fields, and explicit gaps. Bad or missing required data makes the affected analysis unavailable and generally yields `NO_TRADE`.
- Treat model performance as unproven until chronological, cost-aware, leakage-resistant validation supports it. Do not report hypotheses as results.
- Update the relevant docs and `docs/STATUS.md` when an architectural decision changes. Implement only the active milestone in `docs/PLAN.md` unless the user changes scope.
