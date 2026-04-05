# 017: 統計コレクタの修正

## 背景 (Background)

### 現状の問題

`stats.Collector` を中心とした統計収集・表示システムに以下の問題がある。

#### 問題1: `Snapshot()` の観測者効果（深刻度: 高）

`Snapshot()` メソッドがウィンドウの回転（`rotate()`）を引き起こす。つまり、統計を読み取る行為そのものが内部状態を変更してしまう。

```go
// stats.go L76-91
func (c *Collector) Snapshot() map[string]CommandStat {
    now := c.nowFunc()
    if now.After(c.windowEnd) {
        c.rotate()                    // ← 読み取り時に状態変更
        c.windowEnd = now.Add(time.Second)
    }
    // ... snapshot を返す
}
```

HTTP エンドポイントへのリクエスト（`GetStats()` → `Snapshot()`）がウィンドウを回転させるため、リクエストのタイミングによって取得されるデータが変わる。

#### 問題2: コマンドエントリの消失（深刻度: 高）

`rotate()` は `snapshot` を `current` の内容で**完全に置き換える**。あるウィンドウ内で特定のコマンドが一度も呼ばれなかった場合、そのコマンドはスナップショットから完全に消える。

```go
// stats.go L93-108
func (c *Collector) rotate() {
    snap := make(map[string]CommandStat, len(c.current))
    // current に存在するコマンドのみ snap に入る
    c.snapshot = snap        // ← 前回の snapshot は完全に破棄
    c.current = make(map[string]*windowAccum)
}
```

これにより、stats CLI で表示されるコマンド種別数が毎回変動する（ユーザー報告の直接的な原因）。

#### 問題3: ウィンドウ境界のドリフト（深刻度: 中）

`windowEnd` は `now + 1s` で設定されるため、固定の1秒間隔ではなく、呼び出し時刻によってドリフトする。`Record()` や `Snapshot()` のタイミングに依存して、ウィンドウが 0.9秒だったり 1.1秒だったりする。

#### 問題4: モニタのフレーム二重カウント（深刻度: 中）

`RecordFrame()` が2箇所で呼ばれている：

1. `receiveLoop` 内の `vram_update` メッセージ受信時（monitor.go L243）
2. `onPaint` 内（monitor.go L364）

実ウィンドウモードでは FPS が実際の約2倍の値になる。また、`receiveLoop` での `RecordFrame()` は「VRAMイベント受信数」であり、「画面描画フレーム数」とは本質的に異なる。

## 要件 (Requirements)

### 必須要件

1. **Snapshot() の副作用除去**
   - `Snapshot()` 呼び出し時にウィンドウ回転（`rotate()`）を行わない
   - ウィンドウの回転は `Record()` 呼び出し時のみに限定する
   - `Snapshot()` は任意のタイミングで何度呼んでも、同一ウィンドウ期間内では同じ結果を返す

2. **コマンドエントリの安定表示**
   - 一度記録されたコマンド名は、以降のスナップショットから消えないようにする
   - 該当ウィンドウでコマンドが呼ばれなかった場合は `Count: 0, AvgNs: 0, MaxNs: 0` として保持する
   - これにより stats CLI の表示要素数が安定する

3. **ウィンドウ境界の固定化**
   - ウィンドウ境界を呼び出し時刻依存ではなく、固定間隔のタイムスロットに揃える
   - 例: T=0s, T=1s, T=2s... のように壁時計の秒境界に合わせる

4. **複数ウィンドウ期間の統計**
   - 以下の3種類のウィンドウ期間の統計を提供する:
     - **1秒統計**: 直近1秒間の統計（現行相当）
     - **10秒統計**: 直近10秒間の統計（10個の1秒ウィンドウの集約）
     - **30秒統計**: 直近30秒間の統計（30個の1秒ウィンドウの集約）
   - 各ウィンドウ期間で `Count`（合計実行回数）、`AvgNs`（平均実行時間）、`MaxNs`（最大実行時間）を算出する

5. **モニタのフレームカウント整理**
   - `RecordFrame()` の呼び出しを1箇所に整理する
   - `onPaint` での呼び出しのみを正とする（実際の描画フレームを計測）
   - `receiveLoop` での `RecordFrame()` 呼び出しを除去する

### 任意要件

- 統計の永続化は不要（メモリ内のみ）
- バス経由の `stats_data` publish は現行のまま維持

## 実現方針 (Implementation Approach)

### stats.Collector の再設計

#### リングバッファによる固定ウィンドウ管理

```
時間軸 →
|---1s---|---1s---|---1s---|---1s---|  ...  |---1s---|
  slot0    slot1    slot2    slot3          slot29

├── 1秒統計: slot[最新] ──────────────────────────────┤
├── 10秒統計: slot[最新-9] ~ slot[最新] の集約 ────────┤
├── 30秒統計: slot[0] ~ slot[29] の集約 ───────────────┤
```

- 30個の1秒スロットをリングバッファとして保持
- 各スロットは `map[string]*windowAccum` を持つ
- 1秒経過ごとにスロットのインデックスを進め（`Record()` 内で検出）、古いスロットをクリアして新しいウィンドウとする
- `Snapshot()` は副作用なしに、直近の完了スロットから各期間の統計を集約して返す

#### CommandStat の拡張

```go
type WindowStats struct {
    Count int64 `json:"count"`
    AvgNs int64 `json:"avg_ns"`
    MaxNs int64 `json:"max_ns"`
}

type CommandStat struct {
    Last1s  WindowStats `json:"last_1s"`
    Last10s WindowStats `json:"last_10s"`
    Last30s WindowStats `json:"last_30s"`
}
```

#### Snapshot の動作

- `Snapshot()` はロックを取得し、リングバッファの読み取りのみを行う
- ウィンドウ回転は `Record()` でのみ実行される
- 一度でも `Record()` が呼ばれたコマンド名は「既知コマンドセット」として記憶し、`Snapshot()` で常に返す

### statsserver の更新

- レスポンス JSON の構造を新しい `CommandStat` に合わせて更新
- `/stats`, `/stats/vram` のレスポンスで 1s/10s/30s の統計が含まれるようにする

### stats CLI の更新

- 表示を 1s/10s/30s の3カラムグループに拡張
- または `--window` フラグで表示するウィンドウ期間を選択可能にする

### モニタの修正

- `receiveLoop` 内の `m.RecordFrame()` 呼び出しを削除
- `onPaint` 内の `m.RecordFrame()` のみを残す
- ヘッドレスモード時は描画が発生しないため、FPS は 0 となる（仕様として明確化）

## 検証シナリオ (Verification Scenarios)

### シナリオ1: 表示要素数の安定性

1. neurom を起動（`--stats-port 8080`）
2. VRAM に対して `draw_pixel`, `blit_rect`, `set_palette` の3種のコマンドを発行する
3. stats CLI で `--watch` モードで監視する
4. しばらく `draw_pixel` のみを発行する
5. **確認**: `blit_rect` と `set_palette` が表示から消えず、`Count: 0` として残る
6. 再び `blit_rect` を発行する
7. **確認**: `blit_rect` の `Count` が 0 から正の値に変わる

### シナリオ2: Snapshot の冪等性

1. 同一ウィンドウ期間内に `GET /stats` を連続2回呼ぶ
2. **確認**: 両方のレスポンスで同じコマンドセット・同じ `last_1s` 値が返る

### シナリオ3: 複数ウィンドウ期間の正確性

1. 1秒間に `draw_pixel` を 100回発行する
2. 次の1秒間は何も発行しない
3. **確認 (1秒統計)**: 何も発行しなかった秒の `last_1s.count` が 0
4. **確認 (10秒統計)**: `last_10s.count` が 100（まだ10秒以内なので）
5. 10秒後: **確認**: `last_10s.count` が 0 になる

### シナリオ4: モニタ FPS の正確性

1. ヘッドレスモードで起動
2. **確認**: FPS が 0（onPaint が呼ばれないため）
3. （実ウィンドウモードでは描画フレーム数に一致する FPS が得られること）

## テスト項目 (Testing for the Requirements)

### 単体テスト (`stats/stats_test.go`)

| 要件 | テストケース | 検証内容 |
|------|-------------|---------|
| 要件1 | `TestSnapshot_NoSideEffect` | `Snapshot()` を2回呼んで同じ結果が返ること、`Snapshot()` だけではウィンドウが回転しないこと |
| 要件2 | `TestCommandEntry_Persists` | 一度記録したコマンドが、次のウィンドウで呼ばれなくても `Count: 0` で残ること |
| 要件3 | `TestWindowBoundary_Fixed` | ウィンドウ境界が固定1秒間隔であること（nowFunc で時刻制御して検証） |
| 要件4 | `TestMultiWindow_1s_10s_30s` | 1s/10s/30s の各統計が正しく集約されること |
| 要件4 | `TestMultiWindow_Rollover` | 30スロット分の古いデータが正しく消えること |

### 単体テスト (`monitor/monitor_test.go`)

| 要件 | テストケース | 検証内容 |
|------|-------------|---------|
| 要件5 | `TestRecordFrame_SingleSource` | `receiveLoop` で `RecordFrame` が呼ばれないこと |

### 統合テスト (`integration/`)

| 要件 | テストケース | 検証内容 |
|------|-------------|---------|
| 要件1,2 | `TestHTTPStats_StableEntries` | HTTP 経由で取得した stats のコマンドセットが複数回のリクエストで安定すること |
| 要件4 | `TestHTTPStats_MultiWindow` | HTTP レスポンスに `last_1s`, `last_10s`, `last_30s` が含まれること |

### 検証コマンド

```bash
# 全体ビルド + 単体テスト
scripts/process/build.sh

# 統合テスト
scripts/process/integration_test.sh
```
