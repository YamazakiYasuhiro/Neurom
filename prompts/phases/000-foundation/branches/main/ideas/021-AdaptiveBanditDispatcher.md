# 021-AdaptiveBanditDispatcher

## 背景 (Background)

### 現在の戦略体系

018-VRAMDynamicWorkerStrategy で導入し 019-DispatcherFastPath で最適化した `dynamicDispatcher` は、コマンドごとの閾値をベースにワーカー数を「1 か maxWorkers か」の二値で判定するヒューリスティック方式である。

```
newDispatchStrategy(strategy, maxWorkers)
├── "static"  → staticDispatcher   （常に maxWorkers）
└── "dynamic" → dynamicDispatcher  （閾値ベースの二値判定）
```

### dynamicDispatcher の構造的限界

ロングラン環境での運用を通じて、以下の本質的な問題が明らかになった。

#### 1. パフォーマンス劣化時のロールバック機構がない

現在のフィードバックループは「改善を前提」に設計されている。閾値は「並列化の効果が低ければ引き上げ、高ければ引き下げ」の一方向的な調整のみで、**環境変化（サーマルスロットリング、メモリプレッシャー、他プロセスの負荷増加）により並列性能が劣化した場合に元に戻る仕組みがない。**

具体的な問題:
- `singleEMA` は `workers == 1` のときしか更新されない → 閾値が低く並列実行が続く状態では **シングルスレッドのベースラインが古いまま凍結** される
- 並列実行の絶対的な性能劣化を追跡していない → 「以前は速かったが今は遅い」を検知できない
- `thresholdStep = 0.2`（20%ずつ変動）のため回復が遅い

#### 2. ワーカー数の二値判定

`Decide()` は `1` か `maxWorkers` のどちらかしか返さない。実際には、データサイズによっては中間のワーカー数（2, 4 など）が最適な場合がある。仕様 018 の任意要件6「段階的な goroutine 数」が現在のアーキテクチャでは自然に実現できない。

#### 3. 探索の欠如

閾値調整はフィードバックに依存するが、閾値以上のデータは常に並列、閾値未満のデータは常にシングルで実行される。**最適解が変化した場合にそれを発見する仕組み（探索）がない。**

### 解決アプローチ: Contextual Multi-Armed Bandit

これらの問題は、**Contextual Multi-Armed Bandit（文脈付き多腕バンディット）**アルゴリズムで統一的に解決できる。

- **ロールバック**: EMA による報酬減衰で自然に発生（明示的なロールバックロジック不要）
- **段階的ワーカー数**: 各ワーカー数を「アーム」として扱い、最適な数を学習
- **探索**: ε-greedy の探索項により、環境変化の検知コストがアルゴリズムに内包される

計算コストは現在の閾値アルゴリズムとほぼ同等（map lookup + argmax + EMA 更新）。

### Stats Collector の計測限界

現在の `stats.Collector` は 1 秒スロットのリングバッファ（30 スロット）で時間ウィンドウ統計を提供している。10s/30s ウィンドウでは複数スロットを集約するため低頻度コマンドでも平均が得られるが、以下の問題がある。

#### 低頻度コマンドの計測が不安定

`copy_rect` のように数秒に1回しか実行されないコマンドでは、1s ウィンドウの大半で `Count=0, Avg=0` になる。これは「0μs で実行された」ではなく「実行されなかった」の意味だが、表示上は区別がつかない。30s ウィンドウでも、シーン遷移が発生しない期間ではデータが完全に消失する。

```
copy_rect                    0    0.00      0    0.00    1128  209.37
```

#### 時間ウィンドウに依存しない指標の必要性

時間ウィンドウベースの統計は「スループット」の把握には優れるが、「1回の実行にかかる時間」の直近推定値としては不安定である。adaptive ディスパッチャのバンディットが内部で保持する EMA（コマンド実行ごとに更新される指数移動平均）を stats にも公開すれば、低頻度コマンドでも常に直近の性能推定値が表示可能になる。

## 要件 (Requirements)

### 必須要件

1. **戦略名の整理**: 既存の戦略名を以下のように整理する
   - `static` → 変更なし（常に maxWorkers を使用）
   - `dynamic` → `heuristic` にリネーム（後方互換性のため `dynamic` も `heuristic` のエイリアスとして受け付ける）
   - **新規** `adaptive` → Contextual Bandit ベースの新戦略
   - デフォルト: `adaptive`

2. **Contextual Bandit ディスパッチャの実装**:
   - コンテキスト: `(command, dataSizeBucket)` の組み合わせ
   - アーム（選択肢）: 利用可能なワーカー数（`1, 2, 4, ..., maxWorkers` の2のべき乗列）
   - 報酬: スループット（`dataSize / elapsed` — ピクセル/ナノ秒）
   - アルゴリズム: **ε-greedy with Exponential Moving Average**

3. **データサイズのバケット化**:
   - 対数スケールでバケットに分類する
   - バケット定義（6段階）:
     | バケット | 範囲 (ピクセル) | 代表的なケース |
     |:---|:---|:---|
     | 0 | < 256 | 小さい blit (16×16 未満) |
     | 1 | 256 – 1,023 | 小〜中 blit (16×16 〜 32×32) |
     | 2 | 1,024 – 4,095 | 中 blit (32×32 〜 64×64) |
     | 3 | 4,096 – 16,383 | 大 blit / 小 clear (64×64 〜 128×128) |
     | 4 | 16,384 – 65,535 | 大 clear (128×128 〜 256×256) |
     | 5 | 65,536+ | 全画面操作 (256×256+) |

4. **ε-greedy による探索と活用**:
   - 確率 `ε` でランダムにアームを選択（探索）
   - 確率 `1 - ε` で最高報酬のアームを選択（活用）
   - `ε` のデフォルト値: `0.05`（5%）
   - `ε` は定数とし、実行中は変更しない

5. **EMA による報酬追跡と自動ロールバック**:
   - 各アームの報酬を EMA（指数移動平均）で追跡する
   - EMA の減衰係数 `α`: `0.05`
   - EMA により直近の性能に重みが置かれるため、性能劣化時は自然に他のアームの報酬が相対的に高くなり、**ロールバックが暗黙的に発生する**
   - 明示的なロールバックロジックは不要

6. **ファストパスの維持**: 019-DispatcherFastPath で導入されたファストパス（`dataSize < minThreshold` の場合は戦略判定をスキップ）は `adaptiveDispatcher` でも維持する。ファストパスの判定は `parallelRows()` 側で行われるため、ディスパッチャ側での対応は不要

7. **既存テストの互換性**: `dispatcher_test.go` の既存テストについて、`dynamicDispatcher` のテストはそのまま維持し、`adaptiveDispatcher` 用のテストを新規追加する。`newDispatchStrategy` のファクトリテストは新しい戦略名に対応させる

8. **フィードバックのサンプリング**: 高頻度コマンド（blit_rect 等）のオーバーヘッドを抑えるため、Feedback もサンプリングベースとする。全呼び出しで Feedback を処理するのではなく、`feedbackSampleRate` 回に 1 回だけ EMA を更新する

9. **Stats への EMA カラム追加**:
   - `stats.CommandStat` に EMA ベースの統計フィールドを追加する
   - 各コマンドの `Record()` 呼び出し時に、実行時間の EMA を更新する
   - EMA 値はコマンドが実行されない間も保持される（時間ウィンドウで消失しない）
   - `Snapshot()` の返り値に EMA 値を含める

10. **Stats EMA の具体仕様**:
    - 新規フィールド: `CommandStat.EMA` に `EmaNs int64`（EMA の平均実行時間、ナノ秒）を追加
    - 減衰係数 `α = 0.05`（adaptiveDispatcher と同値）
    - 初回実行時は実行時間をそのまま EMA 初期値とする
    - 2回目以降: `ema = (1-α) * ema + α * elapsed_ns`
    - EMA は全戦略（static / heuristic / adaptive）で共通に動作する（Stats Collector のレイヤで実装するため、ディスパッチャに依存しない）

11. **Stats CLI の表示更新**:
    - 既存の 1s / 10s / 30s カラムに加え、EMA カラムを追加する
    - 表示フォーマット:
      ```
      Command          ──── 1s ────   ──── 30s ────   ── EMA ──
                       Count Avg(μs)  Count Avg(μs)   Avg(μs)
      copy_rect            0    0.00   1128  209.37    205.12
      blit_rect        11305    5.57  97543    7.11      6.89
      ```
    - EMA 値は常に表示される（コマンドが1回でも実行されていれば 0 にならない）

12. **Stats HTTP レスポンスの更新**:
    - `/stats` および `/stats/vram` のレスポンス JSON に EMA フィールドを含める
    - JSON 構造の例:
      ```json
      {
        "last_1s": {"count": 0, "avg_ns": 0, "max_ns": 0},
        "last_10s": {"count": 500, "avg_ns": 7110, "max_ns": 15200},
        "last_30s": {"count": 97543, "avg_ns": 7110, "max_ns": 15200},
        "ema_ns": 6890
      }
      ```

### 任意要件

13. **ウォームアップ戦略**: 各アームの試行回数が一定数（例: 10 回）に満たない場合、純粋な探索（ラウンドロビンまたはランダム）を行い、EMA の初期値を安定させる

14. **CLI によるε値のオーバーライド**: `-vram-epsilon` フラグで探索率を変更可能にする（デバッグ・チューニング用途）

## 実現方針 (Implementation Approach)

### アーキテクチャ概要

```
newDispatchStrategy(strategy, maxWorkers)
├── "static"                → staticDispatcher        （変更なし）
├── "heuristic" / "dynamic" → dynamicDispatcher        （リネームのみ）
└── "adaptive" / ""         → adaptiveDispatcher        （新規）
    ├── bandits map[string]*bandit                       （key: "command:bucket"）
    │   └── arms  map[int]*armStats                      （key: worker count）
    │       ├── emaReward float64                         （EMA of throughput）
    │       └── trials    int                             （trial count）
    ├── epsilon   float64                                 （exploration rate）
    └── workerSet []int                                   （[1, 2, 4, ..., maxWorkers]）
```

### 主要コンポーネント

#### 1. データ構造

```go
type armStats struct {
    emaReward float64  // EMA of throughput (dataSize / elapsed_ns)
    trials    int      // number of trials for this arm
}

type bandit struct {
    arms map[int]*armStats  // key = worker count
}

type adaptiveDispatcher struct {
    maxWorkers int
    workerSet  []int               // [1, 2, 4, ..., maxWorkers]
    bandits    map[string]*bandit  // key = "command:bucket"
    epsilon    float64             // exploration rate (default 0.05)
    callCount  uint64              // global call counter for sampling
    mu         sync.Mutex
}
```

#### 2. ワーカー数の候補生成

`maxWorkers` から2のべき乗列を生成する:

```go
func buildWorkerSet(maxWorkers int) []int {
    set := []int{1}
    w := 2
    for w < maxWorkers {
        set = append(set, w)
        w *= 2
    }
    if maxWorkers > 1 {
        set = append(set, maxWorkers)
    }
    return set  // e.g. maxWorkers=8 → [1, 2, 4, 8]
}
```

#### 3. バケット化

```go
func sizeBucket(dataSize int) int {
    switch {
    case dataSize < 256:
        return 0
    case dataSize < 1024:
        return 1
    case dataSize < 4096:
        return 2
    case dataSize < 16384:
        return 3
    case dataSize < 65536:
        return 4
    default:
        return 5
    }
}
```

#### 4. Decide ロジック

```go
func (d *adaptiveDispatcher) Decide(command string, dataSize int) int {
    d.mu.Lock()
    defer d.mu.Unlock()

    bucket := sizeBucket(dataSize)
    key := command + ":" + strconv.Itoa(bucket)
    b := d.getBandit(key)

    // Warmup: if any arm has < warmupTrials, round-robin
    if d.needsWarmup(b) {
        return d.warmupArm(b)
    }

    // ε-greedy
    if rand() < d.epsilon {
        return d.randomArm()
    }
    return d.bestArm(b)
}
```

#### 5. Feedback ロジック

```go
func (d *adaptiveDispatcher) Feedback(command string, dataSize int, workers int, elapsed time.Duration) {
    if dataSize <= 0 || elapsed <= 0 {
        return
    }

    d.mu.Lock()
    defer d.mu.Unlock()

    d.callCount++
    if d.callCount % feedbackSampleRate != 0 {
        return
    }

    bucket := sizeBucket(dataSize)
    key := command + ":" + strconv.Itoa(bucket)
    b := d.getBandit(key)

    reward := float64(dataSize) / float64(elapsed.Nanoseconds())
    arm := b.arms[workers]
    if arm.trials == 0 {
        arm.emaReward = reward
    } else {
        arm.emaReward = (1 - emaAlpha) * arm.emaReward + emaAlpha * reward
    }
    arm.trials++
}
```

#### 6. newDispatchStrategy の更新

```go
func newDispatchStrategy(strategy string, maxWorkers int) dispatchStrategy {
    switch strategy {
    case "static":
        return &staticDispatcher{maxWorkers: maxWorkers}
    case "heuristic", "dynamic":
        return newDynamicDispatcher(maxWorkers)
    default:  // "adaptive", "", or unknown
        return newAdaptiveDispatcher(maxWorkers)
    }
}
```

#### 7. CLI の更新

```go
vramStrategy := flag.String("vram-strategy", "adaptive",
    "VRAM parallelization strategy: static, heuristic, or adaptive")
```

### 定数の設計

| 定数名 | 値 | 根拠 |
|:---|:---|:---|
| `emaAlpha` | 0.05 | 直近の変化を緩やかに追従。急激なノイズに振られない |
| `epsilon` | 0.05 | 探索率 5%。100 回中 5 回はランダム探索 |
| `warmupTrials` | 10 | 各アームの初期サンプル数。EMA の安定化に必要な最小試行 |
| `feedbackSampleRate` | 64 | 019 と同じ。高頻度コマンドのオーバーヘッドを 1/64 に抑制 |
| `numBuckets` | 6 | 対数スケール 6 段階。VGA 解像度範囲をカバー |

### メモリ使用量の見積もり

- コマンド種類: 4 (clear_vram, blit_rect, blit_rect_transform, copy_rect)
- バケット数: 6
- アーム数: maxWorkers=8 の場合 4 (1,2,4,8)
- 1 アーム: `float64 + int` = 16 bytes
- 合計: 4 × 6 × 4 × 16 = **1,536 bytes** ≈ 1.5 KB（無視できるレベル）

### Stats Collector への EMA 追加

#### 1. データ構造の変更

```go
// stats.go

type CommandStat struct {
    Last1s  WindowStats `json:"last_1s"`
    Last10s WindowStats `json:"last_10s"`
    Last30s WindowStats `json:"last_30s"`
    EmaNs   int64       `json:"ema_ns"`  // 新規: EMA of execution time (ns)
}

type Collector struct {
    mu        sync.Mutex
    slots     [SlotCount]slotData
    headIdx   int
    knownCmds map[string]struct{}
    nowFunc   func() time.Time
    cmdEMA    map[string]*emaTracker  // 新規: コマンド別 EMA
}

type emaTracker struct {
    value   float64  // current EMA value (nanoseconds)
    seeded  bool     // whether the first sample has been recorded
}
```

#### 2. Record() での EMA 更新

```go
const statsEMAAlpha = 0.05

func (c *Collector) Record(command string, d time.Duration) {
    c.mu.Lock()
    defer c.mu.Unlock()

    // ... 既存のスロット更新ロジック（変更なし） ...

    // EMA 更新
    ns := float64(d.Nanoseconds())
    tracker := c.cmdEMA[command]
    if tracker == nil {
        tracker = &emaTracker{}
        c.cmdEMA[command] = tracker
    }
    if !tracker.seeded {
        tracker.value = ns
        tracker.seeded = true
    } else {
        tracker.value = (1-statsEMAAlpha)*tracker.value + statsEMAAlpha*ns
    }
}
```

#### 3. Snapshot() での EMA 読み取り

```go
func (c *Collector) Snapshot() map[string]CommandStat {
    // ... 既存の集約ロジック ...

    for cmd := range c.knownCmds {
        // ... 既存の s1, s10, s30 集約 ...

        emaNs := int64(0)
        if t := c.cmdEMA[cmd]; t != nil && t.seeded {
            emaNs = int64(t.value)
        }

        result[cmd] = CommandStat{
            Last1s:  s1.toWindowStats(),
            Last10s: s10.toWindowStats(),
            Last30s: s30.toWindowStats(),
            EmaNs:   emaNs,
        }
    }
    return result
}
```

#### 4. Stats CLI の表示変更

```go
// stats CLI (features/stats/cmd/main.go)

func printStats(s *statsResponse) {
    fmt.Printf("%-24s ──── 1s ────   ──── 10s ────   ──── 30s ────   ── EMA ──\n", "Command")
    fmt.Printf("%-24s %5s %7s  %5s %7s   %5s %7s   %7s\n",
        "", "Count", "Avg(μs)", "Count", "Avg(μs)", "Count", "Avg(μs)", "Avg(μs)")

    // ...
    fmt.Printf("%-24s %5d %7.2f  %5d %7.2f   %5d %7.2f   %7.2f\n",
        name,
        st.Last1s.Count, float64(st.Last1s.AvgNs)/1000.0,
        st.Last10s.Count, float64(st.Last10s.AvgNs)/1000.0,
        st.Last30s.Count, float64(st.Last30s.AvgNs)/1000.0,
        float64(st.EmaNs)/1000.0,
    )
}
```

#### EMA と時間ウィンドウ統計の特性比較

| 特性 | 1s/10s/30s ウィンドウ | EMA |
|:---|:---|:---|
| 更新タイミング | Snapshot 時にスロット集約 | Record 時にインクリメンタル更新 |
| 低頻度コマンド | Count=0 時に Avg=0 | 最後の実行時の値を保持 |
| 外れ値の影響 | ウィンドウ内の全サンプルで平均 | α=0.05 で緩やかに追従 |
| 用途 | スループット把握、バースト検知 | 1回あたりのレイテンシ推定 |

### 変更対象ファイル

| ファイル | 変更内容 |
|:---|:---|
| `dispatcher.go` | `adaptiveDispatcher` の追加、`newDispatchStrategy` の更新 |
| `dispatcher_test.go` | `adaptiveDispatcher` のテスト追加、ファクトリテストの更新 |
| `stats.go` | `emaTracker` 追加、`Record()` に EMA 更新、`CommandStat` に `EmaNs` 追加 |
| `stats_test.go` | EMA 関連のテスト追加 |
| `main.go` (neurom) | `-vram-strategy` のデフォルト値とヘルプ文言の更新 |
| `main.go` (stats CLI) | EMA カラムの表示追加、`commandStat` に `EmaNs` フィールド追加 |
| `README.md` | 実行例の更新 |

### 変更しないファイル

- `vram.go` — `parallelRows()` のロジック変更なし（`dispatchStrategy` インタフェース経由のため）
- `worker.go` — ワーカープールの変更なし
- `staticDispatcher` — 変更なし
- `dynamicDispatcher` — リネームのみ（コード変更なし。`heuristic` でも `dynamic` でも同じ型を返す）
- `server.go` (statsserver) — `CommandStat` の JSON シリアライズで `ema_ns` が自動的に含まれるため変更不要

### ロールバック動作の具体例

**シナリオ: CPU=8、clear_vram (全画面 256×212 = 54,272 ピクセル) で環境変化が発生**

1. 初期状態: 各アーム (1,2,4,8) がウォームアップ完了
2. 定常状態: workers=8 の emaReward が最高 → 95% の確率で workers=8 が選択
3. **環境変化発生**（例: 他プロセスが CPU を占有）
   - workers=8 のスループットが低下 → Feedback で emaReward が下がっていく
   - 5% の探索で workers=4 が選択 → 以前より良い結果が出る場合がある
   - workers=4 の emaReward が workers=8 を上回ると、活用フェーズで workers=4 が選ばれるようになる
4. **環境回復**: workers=8 が探索で再び試される → 良い結果なら emaReward が回復 → 自然に workers=8 に戻る

この一連の動作に明示的なロールバックロジックは不要。EMA の減衰と ε-greedy の探索が連携して自然にロールバックと回復を実現する。

## 検証シナリオ (Verification Scenarios)

### シナリオ 1: adaptive 戦略の基本動作

1. `-cpu 8 -vram-strategy adaptive` で neurom を起動する
2. デモプログラムを 30 秒以上実行する
3. stats を取得する
4. clear_vram の Avg(μs) が static (CPU=8) と同等以上の性能であることを確認する
5. blit_rect の Avg(μs) が static (CPU=1) と同等レベル（ファストパスによる）であることを確認する

### シナリオ 2: heuristic 戦略の後方互換性

1. `-cpu 4 -vram-strategy heuristic` で neurom を起動する
2. stats を取得する
3. 従来の dynamic 戦略と同等の結果であることを確認する

### シナリオ 3: dynamic エイリアスの動作

1. `-cpu 4 -vram-strategy dynamic` で neurom を起動する
2. エラーなく起動し、heuristic 戦略が適用されることを確認する

### シナリオ 4: デフォルト戦略の変更

1. `-cpu 8`（`-vram-strategy` 指定なし）で neurom を起動する
2. adaptive 戦略が適用されていることを確認する

### シナリオ 5: ロングラン中のパフォーマンス安定性

1. `-cpu 8 -vram-strategy adaptive` で neurom を起動する
2. デモプログラムを 5 分以上連続実行する
3. 1 分ごとに stats を取得し、FPS と主要コマンドの Avg(μs) を記録する
4. FPS が安定しており、急激な劣化が発生しないことを確認する
5. 性能が一時的に劣化しても、数十秒以内に回復する傾向があることを確認する

### シナリオ 6: static 戦略の維持

1. `-cpu 4 -vram-strategy static` で neurom を起動する
2. stats を取得する
3. 全操作が従来通り全ワーカーで実行されていることを確認する

### シナリオ 7: EMA カラムの低頻度コマンド表示

1. `-cpu 8 -vram-strategy adaptive` で neurom を起動する
2. デモプログラムを 30 秒以上実行する
3. stats CLI で `--watch` モードを起動する
4. `copy_rect` が 1s ウィンドウで `Count=0, Avg=0.00` のタイミングでも、EMA カラムには直近の実行時間推定値が表示されていることを確認する
5. `blit_rect` のような高頻度コマンドでも EMA 値が表示され、30s Avg と近い値であることを確認する

### シナリオ 8: EMA の追従動作

1. `-cpu 8` で neurom を起動する
2. デモプログラムのシーンが変わるタイミング（負荷が変動するポイント）で stats を連続取得する
3. EMA 値が急激にジャンプせず、緩やかに変動していることを確認する
4. 30s Avg が更新されない（Count=0）タイミングでも、EMA 値は前回の値を保持していることを確認する

### シナリオ 9: パフォーマンス評価マトリックス

CPU 数と戦略の全組み合わせ（3×3 = 9 パターン）でデモプログラムを実行し、パフォーマンスを比較・評価する。

#### 実行マトリックス

| # | CPU 数 | 戦略 | 起動コマンド |
|:---|:---|:---|:---|
| 1 | 1 | static | `-cpu 1 -vram-strategy static -stats-port 8088` |
| 2 | 1 | heuristic | `-cpu 1 -vram-strategy heuristic -stats-port 8088` |
| 3 | 1 | adaptive | `-cpu 1 -vram-strategy adaptive -stats-port 8088` |
| 4 | 4 | static | `-cpu 4 -vram-strategy static -stats-port 8088` |
| 5 | 4 | heuristic | `-cpu 4 -vram-strategy heuristic -stats-port 8088` |
| 6 | 4 | adaptive | `-cpu 4 -vram-strategy adaptive -stats-port 8088` |
| 7 | 8 | static | `-cpu 8 -vram-strategy static -stats-port 8088` |
| 8 | 8 | heuristic | `-cpu 8 -vram-strategy heuristic -stats-port 8088` |
| 9 | 8 | adaptive | `-cpu 8 -vram-strategy adaptive -stats-port 8088` |

#### 手順（各パターン共通）

1. neurom を上記コマンドで起動する
2. デモプログラムを起動し、7 シーン循環を 1 周以上（約 60 秒以上）実行する
3. stats CLI で 30s ウィンドウのデータと EMA を取得する
4. neurom を停止する
5. 次のパターンに進む

#### 収集データ

各パターンで以下のデータを記録する:

| 計測項目 | 取得元 |
|:---|:---|
| FPS (30s) | Monitor Stats |
| clear_vram: 30s Count, 30s Avg(μs), EMA(μs) | VRAM Stats |
| blit_rect: 30s Count, 30s Avg(μs), EMA(μs) | VRAM Stats |
| blit_rect_transform: 30s Count, 30s Avg(μs), EMA(μs) | VRAM Stats |
| copy_rect: 30s Count, 30s Avg(μs), EMA(μs) | VRAM Stats |

#### 結果記録テンプレート

```
==========================================
パフォーマンス評価マトリックス結果
==========================================

■ FPS 比較
              static    heuristic  adaptive
CPU=1         ___       ___        ___
CPU=4         ___       ___        ___
CPU=8         ___       ___        ___

■ clear_vram 30s Avg(μs)
              static    heuristic  adaptive
CPU=1         ___       ___        ___
CPU=4         ___       ___        ___
CPU=8         ___       ___        ___

■ blit_rect 30s Avg(μs)
              static    heuristic  adaptive
CPU=1         ___       ___        ___
CPU=4         ___       ___        ___
CPU=8         ___       ___        ___

■ blit_rect_transform 30s Avg(μs)
              static    heuristic  adaptive
CPU=1         ___       ___        ___
CPU=4         ___       ___        ___
CPU=8         ___       ___        ___

■ copy_rect 30s Avg(μs)
              static    heuristic  adaptive
CPU=1         ___       ___        ___
CPU=4         ___       ___        ___
CPU=8         ___       ___        ___

■ EMA Avg(μs) — 全コマンド
              static    heuristic  adaptive
CPU=1
  clear_vram  ___       ___        ___
  blit_rect   ___       ___        ___
  blit_rt_tx  ___       ___        ___
  copy_rect   ___       ___        ___
CPU=4
  clear_vram  ___       ___        ___
  blit_rect   ___       ___        ___
  blit_rt_tx  ___       ___        ___
  copy_rect   ___       ___        ___
CPU=8
  clear_vram  ___       ___        ___
  blit_rect   ___       ___        ___
  blit_rt_tx  ___       ___        ___
  copy_rect   ___       ___        ___
```

#### 評価基準

以下の観点で結果を評価する:

1. **FPS の安定性**: 全パターンで FPS ≥ 55.0 を維持できているか
2. **adaptive の効果（大データ）**: `clear_vram` で adaptive が static (CPU=1) より高速化されているか
   - 期待: CPU=4/8 の adaptive は CPU=1 の static に対して 2x 以上の高速化
3. **adaptive の効果（小データ）**: `blit_rect` で adaptive が static (CPU=4/8) より悪化していないか
   - 期待: adaptive の blit_rect Avg ≈ static (CPU=1) の Avg（ファストパスにより並列化を回避）
4. **heuristic との比較**: adaptive が heuristic と同等以上のパフォーマンスを示すか
   - 期待: 大データでは同等、小データではファストパスにより同等
5. **CPU=1 でのオーバーヘッド**: CPU=1 の場合、戦略による差がほぼないこと
   - 期待: CPU=1 ではプールが作成されないため全戦略で同一結果
6. **EMA と 30s Avg の整合性**: 高頻度コマンドでは EMA Avg と 30s Avg が近い値であること
   - 期待: 差が 30% 以内

## テスト項目 (Testing for the Requirements)

### 単体テスト

| 要件 | テスト内容 | テストファイル |
|:---|:---|:---|
| 要件1 | `newDispatchStrategy("adaptive", N)` が `*adaptiveDispatcher` を返すこと | `dispatcher_test.go` |
| 要件1 | `newDispatchStrategy("heuristic", N)` が `*dynamicDispatcher` を返すこと | `dispatcher_test.go` |
| 要件1 | `newDispatchStrategy("dynamic", N)` が `*dynamicDispatcher` を返すこと（エイリアス） | `dispatcher_test.go` |
| 要件1 | `newDispatchStrategy("", N)` が `*adaptiveDispatcher` を返すこと（新デフォルト） | `dispatcher_test.go` |
| 要件2 | `adaptiveDispatcher.Decide()` が `workerSet` の範囲内の値を返すこと | `dispatcher_test.go` |
| 要件3 | `sizeBucket()` が各範囲で正しいバケット番号を返すこと | `dispatcher_test.go` |
| 要件4 | ε=1.0 で全選択がランダムになること（探索100%） | `dispatcher_test.go` |
| 要件4 | ε=0.0 で最高報酬のアームが常に選択されること（活用100%） | `dispatcher_test.go` |
| 要件5 | 特定アームの報酬が低下した後、別のアームが選択されるようになること（ロールバック） | `dispatcher_test.go` |
| 要件5 | EMA が `emaAlpha` に従って正しく更新されること | `dispatcher_test.go` |
| 要件7 | 既存の `dynamicDispatcher` テスト（12件）が全 PASS すること | `dispatcher_test.go` |
| 要件8 | `feedbackSampleRate` に従いサンプリングされること | `dispatcher_test.go` |
| 要件9 | `Record()` 呼び出しごとに EMA が更新されること（初回 = 実行時間そのまま） | `stats_test.go` |
| 要件10 | EMA が `α=0.05` に従って正しく減衰・更新されること | `stats_test.go` |
| 要件10 | `Record()` が呼ばれない間、EMA 値が保持されること（0 にならない） | `stats_test.go` |
| 要件10 | `Snapshot()` の返り値に `EmaNs` が含まれ、直近の EMA 値が反映されていること | `stats_test.go` |
| 要件12 | HTTP レスポンスの JSON に `ema_ns` フィールドが含まれること | `stats_http_test.go` |
| 要件13 | ウォームアップ期間中は未試行のアームが優先的に選択されること | `dispatcher_test.go` |

### 統合テスト

| 要件 | テスト内容 | テストファイル |
|:---|:---|:---|
| 要件1 | CLI フラグ `-vram-strategy adaptive` / `heuristic` / `dynamic` / `static` が正しく反映されること | `integration/` |
| 要件9-12 | HTTP `/stats` レスポンスの各コマンドに `ema_ns` が含まれ、1回以上実行済みのコマンドは `ema_ns > 0` であること | `integration/` |

### パフォーマンス評価テスト（手動）

| 要件 | テスト内容 |
|:---|:---|
| 要件全般 | シナリオ 9 のパフォーマンス評価マトリックスを実行し、結果を記録する |
| 要件全般 | 評価基準 1〜6 に照らして結果を判定する |
| 要件全般 | 結果テンプレートを埋めた状態で `tmp/perf_matrix_results.txt` に保存する |

### 検証コマンド

```bash
# 全体ビルド + 単体テスト
./scripts/process/build.sh

# 統合テスト
./scripts/process/integration_test.sh
```
