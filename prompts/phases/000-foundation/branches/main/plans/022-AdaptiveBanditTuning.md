# 022-AdaptiveBanditTuning

> **Source Specification**: [022-AdaptiveBanditTuning.md](file://prompts/phases/000-foundation/ideas/main/022-AdaptiveBanditTuning.md)

## Goal Description

Adaptive Bandit Dispatcher の4つの定数パラメータを変更し、収束速度を大幅に改善する。その後、限定パフォーマンス計測（heuristic/adaptive × cpu=4/cpu=8 の4パターン×180秒）を実行し、チューニング効果を定量的に検証する。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
|:---|:---|
| 要件1: `adaptiveFeedbackSampleRate` 16→1 | Proposed Changes > dispatcher.go |
| 要件2: `warmupTrials` 3→5 | Proposed Changes > dispatcher.go |
| 要件3: `emaAlpha` 0.05→0.15 | Proposed Changes > dispatcher.go |
| 要件4: `defaultEpsilon` 0.05→0.10 | Proposed Changes > dispatcher.go |
| 要件5 (任意): callCount コマンド別分離 | 先送り。feedbackSampleRate=1 の場合は全コールで処理されるため実質的に不要 |
| シナリオ1: ビルドとテスト | Verification Plan > Automated Verification |
| シナリオ2: 限定パフォーマンス評価マトリクス | Proposed Changes > run_perf_matrix_tuned.sh, Verification Plan |
| シナリオ3: 前回結果との比較分析 | Verification Plan > Manual Verification |

## Proposed Changes

### VRAM Dispatcher

#### [MODIFY] [dispatcher_test.go](file://features/neurom/internal/modules/vram/dispatcher_test.go)

* **Description**: `adaptiveFeedbackSampleRate=1` への変更に対応してテストを修正
* **Technical Design**:

  `TestAdaptiveDispatcher_FeedbackSampling` は現在「sampleRate-1 回呼んだ時点で bandits が空であること」を検証している。`sampleRate=1` では初回の Feedback で即座に bandit が作成されるため、このテストのロジックを変更する必要がある。

  ```go
  func TestAdaptiveDispatcher_FeedbackSampling(t *testing.T) {
      d := newAdaptiveDispatcher(4)
      // sampleRate=1 なので、初回 Feedback で即座に bandit が作成される
      d.Feedback("blit_rect", 5000, 1, 10*time.Microsecond)
      if len(d.bandits) == 0 {
          t.Error("bandits should have entry after first feedback")
      }
      // 追加の Feedback でも問題ないことを確認
      d.Feedback("blit_rect", 5000, 1, 10*time.Microsecond)
      key := "blit_rect:3"
      b := d.bandits[key]
      if b == nil {
          t.Fatal("bandit for blit_rect:3 not found")
      }
      arm := b.arms[1]
      if arm == nil || arm.trials != 2 {
          t.Errorf("expected 2 trials, got %v", arm)
      }
  }
  ```

  `TestAdaptiveDispatcher_EMAUpdate` は現在 `adaptiveFeedbackSampleRate` 回分の Feedback を呼んでいる。`sampleRate=1` では 1回で十分だが、テストの意図（EMA が更新されること）は変わらない。ループ回数が 1 になるだけなので、**コード変更なしで PASS する**。

  `TestFeedbackSampling_SkipsMostCalls` は `dynamicDispatcher` のテスト。`feedbackSampleRate=64`（dynamic 側の定数）を参照しているため、adaptive 側の定数変更の影響を受けない。**変更不要**。

#### [MODIFY] [dispatcher.go](file://features/neurom/internal/modules/vram/dispatcher.go)

* **Description**: adaptive dispatcher の定数4つを変更
* **Technical Design**:

  L143-148 の定数ブロックを以下に変更する:

  ```go
  const (
      emaAlpha                   = 0.15  // 変更前: 0.05 — 収束速度3倍
      defaultEpsilon             = 0.10  // 変更前: 0.05 — 探索率向上
      warmupTrials               = 5     // 変更前: 3 — ノイズ耐性向上
      adaptiveFeedbackSampleRate = 1     // 変更前: 16 — 毎回学習
  )
  ```

* **Logic**:
  - `emaAlpha = 0.15`: EMA の63%収束に必要なサンプル数が 20→7 に短縮。低頻度コマンド（clear_vram等）でも数秒で収束可能
  - `defaultEpsilon = 0.10`: 探索率を 5%→10% に引き上げ。誤学習からの回復時間を 18分→12秒に短縮
  - `warmupTrials = 5`: 各アームの初期サンプル数を増やし、ノイズによる誤学習確率を低減
  - `adaptiveFeedbackSampleRate = 1`: 全 Feedback コールで学習を実行。callCount % 1 == 0 は常に true なので、サンプリングスキップが完全に無効化される

---

### パフォーマンス計測スクリプト

#### [NEW] [run_perf_matrix_tuned.sh](file://tmp/run_perf_matrix_tuned.sh)

* **Description**: 4パターン限定の計測スクリプト
* **Technical Design**:

  既存の `tmp/run_perf_matrix.sh` をベースに、以下の変更を加える:
  - 計測対象を heuristic/adaptive × cpu=4/cpu=8 の4パターンに限定
  - 結果出力先を `tmp/result_20260407b.txt` に変更
  - port 番号を 8091-8094 に設定

  ```bash
  #!/bin/bash
  # 4-pattern limited performance matrix (heuristic/adaptive x cpu=4/8)
  # Each cell runs for WAIT_SECONDS (default 180s) so total ~12 min.

  RESULTS_FILE="./tmp/result_20260407b.txt"
  WAIT_SECONDS="${PERF_MATRIX_WAIT_SECONDS:-180}"
  NEUROM_DIR="./features/neurom"
  STATS_DIR="./features/stats"

  echo "=== Performance Evaluation Matrix (Tuned) ===" > "$RESULTS_FILE"
  echo "Date: $(date)" >> "$RESULTS_FILE"
  echo "Per-cell duration: ${WAIT_SECONDS}s" >> "$RESULTS_FILE"
  echo "" >> "$RESULTS_FILE"

  run_test() {
      local cpu=$1
      local strategy=$2
      local port=$3
      echo "Running: cpu=$cpu strategy=$strategy port=$port (${WAIT_SECONDS}s) ..."
      (cd "$NEUROM_DIR" && go run ./cmd/main.go \
          --headless --no-tcp --cpu "$cpu" \
          --vram-strategy "$strategy" --stats-port "$port") \
          > /dev/null 2>&1 &
      local pid=$!
      sleep $WAIT_SECONDS
      echo "--- cpu=$cpu strategy=$strategy ---" >> "$RESULTS_FILE"
      (cd "$STATS_DIR" && go run ./cmd/ \
          --endpoint "http://localhost:$port/stats") \
          >> "$RESULTS_FILE" 2>&1 \
          || echo "  [ERROR fetching stats]" >> "$RESULTS_FILE"
      echo "" >> "$RESULTS_FILE"
      kill $pid 2>/dev/null
      wait $pid 2>/dev/null
      sleep 1
  }

  run_test 4 heuristic 8091
  run_test 4 adaptive  8092
  run_test 8 heuristic 8093
  run_test 8 adaptive  8094

  echo "=== Matrix Complete ===" >> "$RESULTS_FILE"
  echo "Done. Results:"
  cat "$RESULTS_FILE"
  ```

## Step-by-Step Implementation Guide

1. [x] **テストの修正**:
   - `dispatcher_test.go` の `TestAdaptiveDispatcher_FeedbackSampling` を修正
   - `TestFeedbackSampling_SkipsMostCalls` の定数参照を `adaptiveFeedbackSampleRate` → `feedbackSampleRate` に修正

2. [x] **定数の変更**:
   - `dispatcher.go` L143-148 の4つの定数を変更済み

3. [x] **ビルドと単体テスト**:
   - `./scripts/process/build.sh` — 全 PASS

4. [x] **統合テスト**:
   - `./scripts/process/integration_test.sh` — PASS

5. [x] **計測スクリプトの作成**:
   - `tmp/run_perf_matrix_tuned.sh` を作成済み

6. **パフォーマンス計測の実行**:
   - `bash tmp/run_perf_matrix_tuned.sh` を実行
   - 結果が `tmp/result_20260407b.txt` に保存されることを確認

7. **結果の比較分析**:
   - 前回結果（`tmp/result_20260407a.txt`）と新結果を比較
   - 仕様書の判定基準5項目を評価

## Verification Plan

### Automated Verification

1. **Build & Unit Tests**:
   ```bash
   ./scripts/process/build.sh
   ```
   - `dispatcher_test.go` の全テストが PASS すること
   - 特に `TestAdaptiveDispatcher_FeedbackSampling` が新しいロジックで PASS すること

2. **Integration Tests**:
   ```bash
   ./scripts/process/integration_test.sh
   ```
   - 既存の統合テストに regression がないこと

### Manual Verification

3. **パフォーマンス計測**:
   ```bash
   bash tmp/run_perf_matrix_tuned.sh
   ```
   - 4パターン×180秒の計測を完了
   - 結果ファイル `tmp/result_20260407b.txt` で以下を確認:

   | 判定項目 | 条件 |
   |:--|:--|
   | clear_vram EMA (cpu=4, adaptive) | heuristic EMA の 2倍以内 |
   | clear_vram EMA (cpu=8, adaptive) | heuristic EMA の 2倍以内 |
   | blit_rect 30s Count (cpu=4, adaptive) | heuristic Count の 70%以上 |
   | blit_rect 30s Count (cpu=8, adaptive) | heuristic Count の 70%以上 |
   | copy_rect EMA (cpu=4, adaptive) | heuristic EMA の 3倍以内 |

## Documentation

#### [MODIFY] [022-AdaptiveBanditTuning.md](file://prompts/phases/000-foundation/ideas/main/022-AdaptiveBanditTuning.md)
* **更新内容**: 計測結果のサマリーと判定結果を仕様書末尾に追記する（計測完了後）
