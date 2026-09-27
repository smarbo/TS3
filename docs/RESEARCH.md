# Research register

This file separates **engineering fact** (documented source behavior or project contract), **hypothesis** (plausible but unverified prediction), and **empirical result** (measured out-of-sample evidence). There are **no empirical results** yet. Expected usefulness below is a prioritization judgment, not a performance claim. Research must record dataset, available-time semantics, venue/instrument universe, costs, and counterfactual baselines before testing.

## Source selection and candidate inputs

**Engineering fact — V0 source.** Coinbase Exchange documents a public unauthenticated WebSocket feed, `level2` snapshots and absolute-size updates, and a per-product heartbeat. The public `BTC-USD` product endpoint documents metadata fields. See [WebSocket overview](https://docs.cdp.coinbase.com/exchange/websocket-feed/overview), [channels](https://docs.cdp.coinbase.com/exchange/websocket-feed/channels), and [single product endpoint](https://docs.cdp.coinbase.com/api-reference/exchange-api/rest-api/products/get-single-product). This supports a single, liquid, spot USD market with book price and liquidity context while avoiding authentication, derivatives settlement, multi-venue clocks, and REST snapshot alignment. Actual market status and observed data quality must be checked at collection time. Public documentation is a protocol reference, not proof of strategy value.

**V0 accepted: Level 2 plus heartbeat.** Level 2 supplies best bid/ask, spread, displayed depth and book changes at event frequency, useful for market-state and friction hypotheses. It has high message/storage volume and no source event timestamp on the snapshot; updates carry engine `time`. On reconnect, a new snapshot is mandatory. A heartbeat helps identify connection failure but cannot establish that every Level 2 message was captured or that a quiet quote is fresh. Historical coverage exists only after our recorder begins; do not imply public historical Level 2 depth is available. V0 capture over multiple days is needed before feature research. The stated Level 2 delivery guarantee is conditional on an intact source connection and our ability to consume it.

**Next candidate: public trade matches.** Trades add executed price, size, and a maker-side field from which taker direction can be derived. The documented matches channel may drop messages; heartbeat last trade ID and public REST trade retrieval are possible gap tools ([channels](https://docs.cdp.coinbase.com/exchange/websocket-feed/channels)). Engineering cost is gap reconciliation, duplicate handling, REST pagination/rate limits, and source/receipt-time preservation for late backfill. A REST-recovered trade is usable only when fetched, never at its old exchange time. Trade flow might add information beyond the book; that is a hypothesis to test by ablation. Defer to V2 unless V1 evidence needs it.

**OHLCV.** Derive bars from admitted quote midpoints or later trades, preserving construction method and close time. Exchange-provided historical candles may help coarse baselines but often lack publication timing and intrabar sequence; they cannot serve as leakage-safe microstructure replay. Do not mix trade bars and quote bars under one feature name.

**Instrument metadata.** Public product status and increments supply validation and later hypothetical feasibility constraints. Capture response, request/response times, and any subsequent update; historical metadata at a later fetch is not valid for earlier time. Product delisting or status change must remain in the research universe.

**Derivatives context (defer).** Funding, open interest, basis, and liquidations may reflect crowded positioning, carry, or forced flow. Sources differ in publication latency, backfill revisions, methodology, and historical completeness. Liquidation feeds often represent only venue-observed events. Do not implement until a primary-source protocol and available-time archive exist; funding forecasts versus realized payments must be separately labeled.

**Cross-market context (defer).** ETH/BTC, broader market breadth, relative strength, correlation changes, and cross-exchange differences may help distinguish local from broad moves. They introduce asynchronous joins, stale-feed risk, venue basis/fees, and universe survivorship. Add one source at a time and test incremental value after costs and latency.

**Alternative data (reject for initial roadmap).** Macro events, news, sentiment, search trends, on-chain data, and exchange/whale flows may provide slower context, but publication and revision timestamps, paywall/API availability, entity labeling, spam/noise, and survivorship are difficult to reconstruct. Reconsider only with a pre-registered mechanism, timestamp audit, usable historical archive, and ablation against simpler market data. Social sentiment volume alone is not evidence of predictive value.

**Source screening before admission.** For every new feed, record a source card with native fields and units; documented versus *measured* event-to-receive latency; update cadence and missing-message behavior; start/end and completeness of historical coverage; publication/revision semantics; fixed instrument universe and delistings; expected mechanism; noise/failure modes; storage and operations cost; and an incremental ablation plan. For V0 Level 2 and heartbeat, actual latency, cadence variance, retention size, and capture loss are **unknown until measured** in the soak; only their message schemas and nominal heartbeat cadence are documented. Trades have event-level cadence but undetermined capture reliability/history until implemented. Derivatives and alternative data have no accepted historical availability or timestamp contract yet, so none may enter a model. A source without reconstructible `UsableFromTime` is rejected regardless of apparent predictive fit.

V0 source availability now has two separate clocks: source-frame `ReceiveTime` and serialized `AdmissionTime`. The latter, not the former, is the earliest canonical state availability. Durable commit, state application, and publication happen later still and must be measured in the live progress journal. A historical outcome may be evaluated only from a predeclared feasible publication/entry boundary. A state replay hash cannot establish that earlier quote execution was possible. The first 24-hour soak measures these delays but yields **no predictive evidence**. Its absence of detected gaps cannot prove that the provider never silently omitted a book update.

## Feature candidates and mechanisms

**V1 priority, from Level 2:** Midpoint log returns over fixed available-time windows measure recent repricing; robust realized volatility measures current uncertainty and cost of false signals; spread and displayed depth approximate immediate friction and liquidity; short-horizon quote momentum/trend strength may indicate persistent demand; midpoint displacement from a rolling average may indicate temporary overshoot or trend continuation. Book imbalance and microprice may proxy nearby supply/demand, but displayed liquidity can cancel and is not executed flow. Every feature carries horizon, warm-up, feed freshness, units, as-of ordinal, and version. A final bar or window value appears only when its recorded close tick occurs. Use simple definitions and no tuning on final test.

**V2 candidates, conditional on trade capture:** Trade imbalance/aggressive flow and cumulative volume delta may proxy initiated demand; relative volume and acceleration may measure participation; VWAP displacement may proxy inventory or mean reversion. Trade direction semantics must use the documented maker-side convention; missing matches make these unavailable. Compare to quote-only features under identical costs and periods.

**Deferred or cautious:** Open-interest changes/funding/basis can proxy leveraged crowding but publication frequency and revision behavior matter. Liquidation intensity is venue partial and may be reactive. Cross-asset momentum/relative strength and correlation shifts might describe risk appetite, but require synchronized available-time joins and a fixed universe. Avoid overlapping variants of the same indicator unless an ablation finds independent incremental value.

**Feature screening before implementation.** Each proposed feature needs a versioned card specifying required source and its historical coverage, exact formula/units, update cadence and horizon, event versus receive-time rule, maximum input staleness, warm-up, missing/gap behavior, plausible mechanism, likely noise, computation/storage cost, leakage and universe risks, and a predeclared baseline/ablation. Quote returns, trend, displacement, and realized volatility share a Level 2 midpoint source and may be strongly dependent; they must not be counted as separate independent signals without evidence. Spread/depth/microprice/imbalance can update on every book event but reflect *displayed* liquidity, which can vanish; their historical availability begins only with V0 capture. Trade flow and volume features are unavailable until complete-enough match capture is demonstrated. Funding, basis, liquidation, and cross-asset features remain rejected until source cards satisfy publication-time and universe requirements. If a feature's incremental benefit does not survive latency and costs, remove it rather than tuning more variants.

For future V1 research, freeze a reference notional, side-specific entry/exit quotes, quote-generation and age limits, round-trip fees, depth/slippage, latency, horizon start, and missing-endpoint censoring **before** labels are built. Keep as-of revisions of any late-data bars; a final-bar table is not a valid historical feature table. Compare models on a predeclared common health-eligible population, while reporting full-calendar coverage, outage-driven abstention, and excluded-period conditions where independently observable; otherwise mark market outcomes unknown. Outages correlated with volatile adverse markets may make a strategy look selective even when it has no edge. Maintain an experiment ledger of every tried horizon, feature, regime gate, cost assumption and selection rule. Use development walk-forward folds for iteration, and reserve a sealed final period for one frozen evaluation; after viewing it, any redesign needs new future data for a fresh final test. These are methodological requirements, not empirical findings.

Horizon hierarchy: microstructure seconds (fragile, high friction), short minutes (possible momentum or reversal), intraday hours (liquidity/volatility context), broader days (regime). Model each horizon explicitly. A seconds-level indicator is a veto or supporting cue unless a held-out test shows it can improve a longer-horizon decision after costs. No feature receives a universal direction interpretation across regimes without testing.

## Signal-family hypotheses and falsification

### Trend and momentum

- **Hypothesis:** Recent quote/trade repricing predicts same-direction net returns for a defined minutes-to-hours horizon.
- **Possible mechanism:** persistent information arrival or order splitting.
- **Failure regimes:** range-bound, thin, rapidly reversing, high-spread markets.
- **Falsify if:** chronological out-of-sample lift over simple momentum/always-long baselines disappears after latency and costs, or is confined to unstable parameter pockets.

### Mean reversion

- **Hypothesis:** Large displacement from a local reference predicts reversal over minutes to intraday windows.
- **Possible mechanism:** temporary liquidity demand and inventory rebalancing.
- **Failure regimes:** genuine news, strong trends, volatility breaks, impaired liquidity.
- **Falsify if:** conditional reversals do not exceed spread/slippage or depend on knowing the future reference mean.

### Order flow and microstructure

- **Hypothesis:** Persistent signed trade flow or book imbalance predicts near-term repricing beyond current spread.
- **Possible mechanism:** aggressive demand depletes displayed liquidity or reveals informed flow.
- **Horizon:** seconds to minutes; not available from V0 book alone for signed trade flow.
- **Failure regimes:** spoofed/cancelled depth, hidden liquidity, queue changes, delayed capture, fee-dominated small moves.
- **Falsify if:** effect vanishes with realistic capture latency, missed-trade exclusions, quote-based costs, or comparison to simple midpoint momentum.

### Volatility breakout

- **Hypothesis:** A move out of a predeclared range under rising volatility predicts continuation over minutes to hours.
- **Possible mechanism:** stop-driven or risk-limit-driven flow.
- **Failure regimes:** false breaks in illiquid periods, mean-reverting auction states, post-news reversal.
- **Falsify if:** break thresholds are unstable across walk-forward folds or net return is not superior to a simple volatility-conditioned momentum baseline.

### Derivatives positioning

- **Hypothesis:** Extreme or changing funding, basis, and open interest condition future direction or squeeze risk.
- **Possible mechanism:** crowded leverage and forced deleveraging.
- **Horizon:** hours to days, dependent on publication cadence.
- **Failure regimes:** structural basis changes, venue-specific distortions, revised or delayed data.
- **Falsify if:** available-time-aligned out-of-sample lift vanishes against price/volatility-only models or the archive cannot reconstruct publication timing.

### Cross-market confirmation

- **Hypothesis:** Broad BTC/ETH or fixed-universe participation distinguishes market-wide moves from local noise.
- **Possible mechanism:** common risk factor and correlated allocation flows.
- **Horizon:** minutes to days.
- **Failure regimes:** asset-specific catalysts, asynchronous/stale feeds, changing correlations.
- **Falsify if:** fixed-universe, synchronized walk-forward tests show no incremental net benefit over single-market baselines or apparent benefit comes from later-arriving data.

All six are hypotheses. Signal independence is an empirical question: measure shared labels, feature overlap, residual correlation, and family ablations before aggregating probabilities. Disagreement is recorded as a feature or abstention reason, not smoothed away by an arbitrary confidence score.

## Rejected ideas and open questions

- **Rejected now:** indicator zoo, universal confidence number, future-complete candles, restored book across disconnect, zero-cost P&L, complex hidden-state regime model, automatic deep learning, and any claim that a direction forecast implies a worthwhile opportunity.
- **Open:** actual Level 2 capture volume/latency on the intended host; reliability of heartbeat cadence; whether a 30-second quiet-book warning is suitable; storage retention/compression requirements; which fee schedule and hypothetical taker/maker assumption to use in V1; how to obtain a leakage-safe historical trade archive if matches are added; whether quote-only momentum survives realistic friction; how to label outcomes without overlapping-horizon leakage; and what minimum calibration sample is defensible. Resolve with documented observation or a predeclared experiment, not intuition.
