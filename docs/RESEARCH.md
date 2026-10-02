# Research register

This file separates **engineering fact** (documented source behavior or project contract), **hypothesis** (plausible but unverified prediction), and **empirical result** (measured out-of-sample evidence). V0's accepted capture/replay is engineering evidence; there are **no validated predictive results** yet. Expected usefulness below is a prioritization judgment, not a performance claim. Research must record dataset, available-time semantics, venue/instrument universe, costs, and counterfactual baselines before testing. The pre-outcome V1 feature, signal, cost, and evaluation assumptions are frozen in [V1_DESIGN](V1_DESIGN.md). V3 development tests the small predeclared conditional model in [V3_DESIGN](V3_DESIGN.md), subject to the sample limits in [V3_DATA_ASSESSMENT](V3_DATA_ASSESSMENT.md); it does not turn the exposed V0 tape into a sealed test.

**V2 hypotheses, pre-outcome:** persistent 60-second price repricing, 300-second displacement reversion, and 30-second displayed book-depth pressure are three distinct *mechanism candidates*, not proven independent predictors. Trend and reversion share price samples and count as one price vote; book pressure is only a short-horizon confirming vote. Fixed definitions, falsifiers, costs and cohort tests are in [V2_DESIGN](V2_DESIGN.md) and the predeclared [V2 ledger](V2_EXPERIMENT_LEDGER.md). Derivatives, cross-market context and signed trade flow remain unimplemented because this accepted tape cannot validate their live/replay joins. V2 outcomes from the exposed one-day tape will be exploratory development evidence only.

**Exploratory V1 observation, not a validated edge.** The frozen V1 baseline was run twice over the accepted one-day Kraken tape with identical 12,316,574-record reconstruction, V0 hashes, V1 intents, and complete reports. Of 1,446 minute decisions, 1,339 had eligible current quotes and 1,302 had paired hypothetical endpoints. The conservative default policy made zero directional actions because 90 nonflat momentum candidates did not exceed assumed round-trip friction plus margin. The unconstrained research momentum and mean-reversion comparators took 90 and 185 paired hypothetical actions and averaged approximately −48.94 and −50.49 bps net per action under the predeclared 20-bp fee and 5-bp adverse allowance per side. These are assumed-cost, displayed-depth scenarios on exposed development data, not fills, an optimized strategy, or a sealed final test. The zero-action policy and matched random control provide no directional performance estimate. See [V1_FINAL_AUDIT](V1_FINAL_AUDIT.md) for provenance, censoring, uncertainty and exact hashes. No threshold or cost was retuned after observing these results.

**Exploratory V2 result, not a validated edge.** V2-001 kept its frozen three-family/two-mechanism design and replayed the same accepted tape twice with exact V0/V1/V2 parity. The 1,446 decisions were all `NO_TRADE`: 136 explicit disagreements, 31 directional consensus minutes all rejected by the predeclared cost screen, and no deduplicated theses. Those consensus minutes' observed past-move proxy reached at most 28.17 bps against at least 50.01 bps modeled round-trip friction plus a 10-bp margin. Their *hypothetical* five-minute price-vote net averaged -45.60 bps/action after costs. On its separate 30-second paired population, displayed book pressure's 1,070 hypothetical actions averaged +0.28 gross and -49.82 net bps/action, compared with +0.14 gross and -49.96 net for a frequency-matched random-direction control. This tiny descriptive gross difference on dependent observations is not proof of independent predictive information or tradeability. A clean 65-minute public smoke reproduced all 67 V2 intents exactly from raw replay and measured no economic outcome. See [V2_RESEARCH_REPORT](V2_RESEARCH_REPORT.md) and [V2_FINAL_AUDIT](V2_FINAL_AUDIT.md). No V2 threshold, weight or cost was retuned from these outcomes. Further calibration or economic claims require later untouched chronology; V3 was not started.

## Source selection and candidate inputs

**Engineering fact — active V0 source.** Coinbase Exchange's [current WebSocket authentication guide](https://docs.cdp.coinbase.com/exchange/websocket-feed/authentication) requires account authentication for `level2`; an unauthenticated 2026-09-27 probe was rejected. Because the project forbids credentials, V0 uses Kraken Spot's [public WebSocket v2 endpoint](https://docs.kraken.com/exchange/guides/websockets/introduction) `wss://ws.kraken.com/v2`, its [book channel](https://docs.kraken.com/exchange/api-reference/spot-websocket-v2/book) for `BTC/USD` depth 100, and a public [AssetPairs](https://docs.kraken.com/api-reference/market-data/get-tradable-asset-pairs) lookup of `XBTUSD` before subscription. The REST alias maps to the v2 `BTC/USD` symbol. All metadata and source frames enter the ordered raw stream; current product status cannot be projected backward.

**V0 Level 2 source card.** Kraken book snapshots and updates carry source `timestamp`, numeric price/quantity tokens, and a top-ten CRC32 checksum. Updates use absolute size; zero removes a level; the local book must truncate to the subscribed depth after all message changes. Source numeric spelling, including trailing zeroes, is needed for the checksum even when canonical price identity removes them. The channel does not document a contiguous book sequence ID, so checksum mismatch is a detected reconstruction failure, while a provider-side omission that leaves the same top ten may remain undetected. The [automatic heartbeat](https://docs.kraken.com/exchange/api-reference/spot-websocket-v2/heartbeat) appears about once per second only when no other subscribed update arrives; it has no product or source timestamp. Book traffic and heartbeat both establish connection activity, while only book traffic refreshes the displayed quote. The accepted 24-hour-plus run captured 12,316,574 committed ordinals at about 141 frames/s and 94,481 raw bytes/s, with six marked EOF reconnects, zero detected capture gaps/checksum failures/clock anomalies, and exact full replay parity. See [STATUS](STATUS.md) for evidence and limits. These are engineering observations for that run, not strategy value or proof of provider-side completeness.

**Next candidate after V2: public trades.** Executed price/size and aggressor direction may add information beyond displayed book changes, but native side semantics, missing-message behavior, duplicate handling, REST pagination, receipt/publication timing, and archival completeness require a new source card. Any recovered historical trade is usable only when fetched, never at its exchange timestamp. The fixed V2-001 core does not admit this feed.
**OHLCV.** Derive bars from admitted quote midpoints or later trades, preserving construction method and close time. Exchange-provided historical candles may help coarse baselines but often lack publication timing and intrabar sequence; they cannot serve as leakage-safe microstructure replay. Do not mix trade bars and quote bars under one feature name.

**Instrument metadata.** Public product status and increments supply validation and later hypothetical feasibility constraints. Capture response, request/response times, and any subsequent update; historical metadata at a later fetch is not valid for earlier time. Product delisting or status change must remain in the research universe.

**Derivatives context (defer).** Funding, open interest, basis, and liquidations may reflect crowded positioning, carry, or forced flow. Sources differ in publication latency, backfill revisions, methodology, and historical completeness. Liquidation feeds often represent only venue-observed events. Do not implement until a primary-source protocol and available-time archive exist; funding forecasts versus realized payments must be separately labeled.

**Cross-market context (defer).** ETH/BTC, broader market breadth, relative strength, correlation changes, and cross-exchange differences may help distinguish local from broad moves. They introduce asynchronous joins, stale-feed risk, venue basis/fees, and universe survivorship. Add one source at a time and test incremental value after costs and latency.

**Alternative data (reject for initial roadmap).** Macro events, news, sentiment, search trends, on-chain data, and exchange/whale flows may provide slower context, but publication and revision timestamps, paywall/API availability, entity labeling, spam/noise, and survivorship are difficult to reconstruct. Reconsider only with a pre-registered mechanism, timestamp audit, usable historical archive, and ablation against simpler market data. Social sentiment volume alone is not evidence of predictive value.

**Source screening before admission.** For every new feed, record a source card with native fields and units; documented versus *measured* event-to-receive latency; update cadence and missing-message behavior; start/end and completeness of historical coverage; publication/revision semantics; fixed instrument universe and delistings; expected mechanism; noise/failure modes; storage and operations cost; and an incremental ablation plan. The accepted V0 Kraken run measured one day of cadence, storage and operational lag; it cannot measure silent provider-side omissions or prove future reliability. Trades have event-level cadence but undetermined capture reliability/history until implemented. Derivatives and alternative data have no accepted historical availability or timestamp contract yet, so none may enter a model. A source without reconstructible `UsableFromTime` is rejected regardless of apparent predictive fit.

V0 source availability has two separate clocks: source-frame `ReceiveTime` and serialized `AdmissionTime`. The latter, not the former, is the earliest canonical state availability. Durable commit, state application, and publication happen later still and were measured in the live progress journal. A historical outcome may be evaluated only from a predeclared feasible publication/entry boundary. A state replay hash cannot establish that earlier quote execution was possible. The accepted 24-hour soak measured operational delays but yielded **no predictive evidence**. Its absence of detected gaps cannot prove that the provider never silently omitted a book update.

## Feature candidates and mechanisms

**V1 priority, from Level 2:** Midpoint log returns over fixed available-time windows measure recent repricing; robust realized volatility measures current uncertainty and cost of false signals; spread and displayed depth approximate immediate friction and liquidity; short-horizon quote momentum/trend strength may indicate persistent demand; midpoint displacement from a rolling average may indicate temporary overshoot or trend continuation. Book imbalance and microprice may proxy nearby supply/demand, but displayed liquidity can cancel and is not executed flow. Every feature carries horizon, warm-up, feed freshness, units, as-of ordinal, and version. A final bar or window value appears only when its recorded close tick occurs. Use simple definitions and no tuning on final test.

**Deferred candidates, conditional on trade capture:** Trade imbalance/aggressive flow and cumulative volume delta may proxy initiated demand; relative volume and acceleration may measure participation; VWAP displacement may proxy inventory or mean reversion. Trade direction semantics must use the documented maker-side convention; missing matches make these unavailable. Compare to quote-only features under identical costs and periods after a separate source and replay contract exists.

**Deferred or cautious:** Open-interest changes/funding/basis can proxy leveraged crowding but publication frequency and revision behavior matter. Liquidation intensity is venue partial and may be reactive. Cross-asset momentum/relative strength and correlation shifts might describe risk appetite, but require synchronized available-time joins and a fixed universe. Avoid overlapping variants of the same indicator unless an ablation finds independent incremental value.

**Feature screening before implementation.** Each proposed feature needs a versioned card specifying required source and its historical coverage, exact formula/units, update cadence and horizon, event versus receive-time rule, maximum input staleness, warm-up, missing/gap behavior, plausible mechanism, likely noise, computation/storage cost, leakage and universe risks, and a predeclared baseline/ablation. Quote returns, trend, displacement, and realized volatility share a Level 2 midpoint source and may be strongly dependent; they must not be counted as separate independent signals without evidence. Spread/depth/microprice/imbalance can update on every book event but reflect *displayed* liquidity, which can vanish; their historical availability begins only with V0 capture. Trade flow and volume features are unavailable until complete-enough match capture is demonstrated. Funding, basis, liquidation, and cross-asset features remain rejected until source cards satisfy publication-time and universe requirements. If a feature's incremental benefit does not survive latency and costs, remove it rather than tuning more variants.

For V1 research, the reference notional, side-specific entry/exit quotes, quote-generation and age limits, round-trip fees, depth/slippage, latency, horizon start, and missing-endpoint censoring were frozen in [V1_DESIGN](V1_DESIGN.md) **before** full-tape labels were built. Keep as-of revisions of any late-data bars; a final-bar table is not a valid historical feature table. Compare models on a predeclared common health-eligible population, while reporting full-calendar coverage, outage-driven abstention, and excluded-period conditions where independently observable; otherwise mark market outcomes unknown. Outages correlated with volatile adverse markets may make a strategy look selective even when it has no edge. Maintain an experiment ledger of every tried horizon, feature, regime gate, cost assumption and selection rule. Use development walk-forward folds for iteration, and reserve a sealed final period for one frozen evaluation; after viewing it, any redesign needs new future data for a fresh final test. These are methodological requirements, not empirical findings.

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
- **Open:** future reliability of heartbeat cadence and quiet-book warning thresholds; storage retention/compression; current venue-specific fees beyond V1's declared hypothetical 20-bp taker scenario; how to obtain a leakage-safe historical trade archive if matches are added; whether quote-only momentum survives realistic friction in untouched periods; how to handle overlapping-label dependence and what minimum calibration sample is defensible. Resolve with documented observation or a predeclared experiment, not intuition.
