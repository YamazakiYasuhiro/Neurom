# 022-AdaptiveBanditTuning

## 背景 (Background)

### 前回計測の結果と問題

[021-AdaptiveBanditDispatcher](file://prompts/phases/000-foundation/ideas/main/021-AdaptiveBanditDispatcher.md) で導入した adaptive 戦略（Contextual Multi-Armed Bandit）のパフォーマンス評価（[result_20260407a.txt](file://tmp/result_20260407a.txt)、9パターン×180秒）において、以下の深刻な問題が判明した。

#### 180秒後のEMAで収束が確認できない

| コマンド | heuristic EMA(μs) | adaptive EMA(μs) | 差異 |
|:--|--:|--:|:--|
| clear_vram (cpu=4) | 45.27 | **168.65** | 3.7倍悪い |
| clear_vram (cpu=8) | 33.63 | **114.82** | 3.4倍悪い |
| copy_rect (cpu=4) | 60.49 | **192.44** | 3.2倍悪い |
| blit_rect (cpu=4) | 0.03 | **24.20** | 807倍悪い |

Stats CollectorのEMA（α=0.05、全コール毎更新）は直近の傾向を反映する指標である。**180秒後のEMAがこの値ということは、テスト末尾でもadaptiveは収束していない。**

### 根本原因の分析

数学的分析により、以下の3つの構造的問題を特定した。

#### 1. `callCount` のグローバル共有による学習機会の不均等

`callCount` は全コマンドで共有される単一カウンタ（[dispatcher.go L286](file://features/neurom/internal/modules/vram/dispatcher.go#L286)）。blit_rect が全体の約93%を占めるため、clear_vram は全feedbackの **約1.4%** しか受け取れない。

```
全parallelRows呼出: ~3,826/s
├── blit_rect:           ~3,548/s (92.8%)
├── blit_rect_transform: ~150/s   (3.9%)
├── copy_rect:           ~76/s    (2.0%)
└── clear_vram:          ~52/s    (1.4%)

Feedback処理 (feedbackSampleRate=16): 3,826/16 ≈ 239回/s
→ clear_vram が得るFeedback: ~3.3回/s
```

#### 2. ウォームアップ3サンプルでの誤学習リスク

`warmupTrials=3` では測定ノイズに脆弱。3回の計測で誤ったアームに固着した場合、回復には非bestアームへのfeedbackが必要だが、ε=0.05の探索率では:

```
非bestアーム feedback = 0.05/3 × 3.3 ≈ 0.055回/s
60サンプルで修正 → 60 / 0.055 ≈ 1,091秒 ≈ 約18分
```

**180秒のテストでは誤学習からの回復が不可能。**

#### 3. EMA α=0.05 が低頻度コマンドに対して保守的すぎる

α=0.05 で95%収束には約60サンプルが必要。clear_vramの feedback が ~3.3回/s の場合、収束に ~18秒かかる。ウォームアップで誤学習した場合、この遅い収束速度がさらに回復を遅延させる。

## 要件 (Requirements)

### 必須要件

1. **`adaptiveFeedbackSampleRate` の変更**: `16` → `1` に変更する
   - 毎回の `Feedback()` で学習を実行する
   - 高頻度コマンドのオーバーヘッドは `d.mu.Lock()` の mutex 取得コスト分のみ増加するが、もともと `parallelRows()` 内で `v.mu.Lock()` が取得済みの状態で呼ばれるため、外部からの競合は発生しない
   - これにより clear_vram の feedback レートが **3.3回/s → 52回/s** に向上する

2. **`warmupTrials` の変更**: `3` → `5` に変更する
   - ノイズ耐性を向上させ、初期誤学習の確率を低減する
   - ウォームアップ完了に必要なfeedback数は増加するが（cpu=4: 9→15, cpu=8: 12→20）、`feedbackSampleRate=1` との組み合わせにより時間的には短縮される

3. **`emaAlpha` の変更**: `0.05` → `0.15` に変更する
   - 収束速度を約3倍に高速化（63%収束: 20サンプル→7サンプル）
   - 低頻度コマンドでも sec 単位で収束可能になる
   - ノイズへの感度が上がるが、`warmupTrials=5` との組み合わせで初期値が安定するため問題ない

4. **`defaultEpsilon` の変更**: `0.05` → `0.10` に変更する
   - 探索率を上げて誤学習の早期検出・修正を促進する
   - 誤学習回復時間の短縮: 非bestアームへの feedback 増加により、環境変化への追従も高速化
   - トレードオフ: 定常状態での 10% のランダム選択による微小なオーバーヘッド増加（従来5%→10%）

### 任意要件

5. **`callCount` のコマンド別分離**: グローバルな `callCount` をコマンド別のカウンタに変更し、各コマンドが均等にfeedbackを受け取れるようにする
   - `feedbackSampleRate=1` の場合は実質的に不要（全コールでfeedback処理されるため）
   - 将来 `feedbackSampleRate > 1` に戻す場合に備えた改善

### パラメータ変更サマリー

| パラメータ | 変更前 | 変更後 | 変更理由 |
|:--|--:|--:|:--|
| `adaptiveFeedbackSampleRate` | 16 | **1** | 全コールで学習、低頻度コマンドの学習加速 |
| `warmupTrials` | 3 | **5** | ノイズ耐性向上、初期誤学習リスク低減 |
| `emaAlpha` | 0.05 | **0.15** | 収束速度3倍、環境変化への追従高速化 |
| `defaultEpsilon` | 0.05 | **0.10** | 探索率向上、誤学習からの回復高速化 |

### 期待される収束時間

| コンポーネント | 変更前 (cpu=4) | 変更後 (cpu=4) |
|:--|--:|--:|
| ウォームアップ (clear_vram) | ~2.8秒 | ~0.29秒 |
| 63% EMA収束 | ~6.5秒 | ~0.13秒 |
| 95% EMA収束 | ~19秒 | ~0.41秒 |
| 誤学習回復（最悪ケース） | ~18分 | **~12秒** |

## 実現方針 (Implementation Approach)

### 変更対象ファイル

| ファイル | 変更内容 |
|:--|:--|
| [dispatcher.go](file://features/neurom/internal/modules/vram/dispatcher.go) | 4つの定数値を変更するのみ |

### 具体的な変更箇所

```diff
 // --- adaptive (Contextual Multi-Armed Bandit) ---

 const (
-	emaAlpha                   = 0.05
+	emaAlpha                   = 0.15
-	defaultEpsilon             = 0.05
+	defaultEpsilon             = 0.10
-	warmupTrials               = 3
+	warmupTrials               = 5
-	adaptiveFeedbackSampleRate = 16
+	adaptiveFeedbackSampleRate = 1
 )
```

### 変更しないファイル

- `vram.go` — 変更なし
- `worker.go` — 変更なし
- `stats.go` — 変更なし
- `dispatcher_test.go` — 既存テストはパラメータに依存しない設計のため、定数変更でも PASS するはず。ただしビルドで確認する

## 検証シナリオ (Verification Scenarios)

### シナリオ 1: パラメータ変更後のビルドとテスト

1. `dispatcher.go` の4つの定数を変更する
2. `./scripts/process/build.sh` を実行し、ビルドと全単体テストが PASS することを確認する
3. `./scripts/process/integration_test.sh` を実行し、全統合テストが PASS することを確認する

### シナリオ 2: 限定パフォーマンス評価マトリクス（4パターン×180秒）

前回は 3×3=9 パターンを計測したが、今回は以下の4パターンに限定して再計測する:

| # | CPU | Strategy | Port | 選定理由 |
|:--|:--|:--|:--|:--|
| 1 | 4 | heuristic | 8091 | ベースライン（前回最良）|
| 2 | 4 | adaptive | 8092 | チューニング効果の検証 |
| 3 | 8 | heuristic | 8093 | ベースライン（前回最良）|
| 4 | 8 | adaptive | 8094 | チューニング効果の検証 |

各パターン180秒、合計 ~12分。

#### 手順

1. 既存の `tmp/run_perf_matrix.sh` をベースに、4パターンのみ実行するスクリプト `tmp/run_perf_matrix_tuned.sh` を作成する
2. スクリプトを実行する
3. 結果を `tmp/result_20260407b.txt` に保存する
4. 前回の結果（`tmp/result_20260407a.txt`）と比較する

#### 判定基準

以下の全条件を満たすことで、チューニングの成功とする:

| 判定項目 | 条件 |
|:--|:--|
| clear_vram EMA (cpu=4, adaptive) | heuristic の EMA の **2倍以内** であること（前回は3.7倍悪かった） |
| clear_vram EMA (cpu=8, adaptive) | heuristic の EMA の **2倍以内** であること（前回は3.4倍悪かった） |
| blit_rect 30s Count (cpu=4, adaptive) | heuristic の Count の **70%以上** であること（スループット大幅劣化がないこと） |
| blit_rect 30s Count (cpu=8, adaptive) | heuristic の Count の **70%以上** であること |
| copy_rect EMA (cpu=4, adaptive) | heuristic の EMA の **3倍以内** であること（前回は3.2倍悪かった） |

### シナリオ 3: 前回結果との比較分析

再計測結果を前回の結果と並べて比較し、以下を評価する:

1. adaptive の EMA 値が全コマンドで改善しているか
2. 30s ウィンドウの平均値も改善しているか
3. heuristic との差が縮小しているか
4. heuristic 単体の性能に変化がないこと（regression がないこと）

## テスト項目 (Testing for the Requirements)

### 自動テスト（ビルドパイプライン）

| 要件 | テスト内容 | 検証コマンド |
|:--|:--|:--|
| 要件1-4 | 定数変更後のビルドが成功すること | `./scripts/process/build.sh` |
| 要件1-4 | 既存の `dispatcher_test.go` が全 PASS すること | `./scripts/process/build.sh` |
| 要件1-4 | 既存の統合テストが全 PASS すること | `./scripts/process/integration_test.sh` |

### パフォーマンス検証（計測スクリプト）

| 要件 | テスト内容 | 検証方法 |
|:--|:--|:--|
| 要件全般 | シナリオ 2 の4パターン計測を実行 | `bash tmp/run_perf_matrix_tuned.sh` |
| 要件全般 | シナリオ 2 の5つの判定基準を全て満たすこと | 結果ファイルの目視確認 |
| 要件全般 | シナリオ 3 の前回比較で regression がないこと | 前回結果との比較 |

### 検証コマンド

```bash
# 全体ビルド + 単体テスト
./scripts/process/build.sh

# 統合テスト
./scripts/process/integration_test.sh

# パフォーマンス計測（4パターン×180秒、約12分）
bash tmp/run_perf_matrix_tuned.sh
```
