# 015: パフォーマンス計測とスタット取得

## 背景 (Background)

現在のNeuromシステムでは、各モジュールの処理パフォーマンスを計測する仕組みが存在しない。VRAMの各コマンド（`blit_rect`, `clear_vram`, `blit_rect_transform` 等）がどの程度の時間を要しているか、Monitorが何FPSで描画しているかを把握する手段がなく、パフォーマンスチューニング（例: 014-VRAMMultiCore のような最適化）の効果を定量的に評価できない。

外部からこれらの統計情報を取得・表示するツールも存在しないため、開発者がリアルタイムでパフォーマンスを監視することができない。

## 要件 (Requirements)

### 必須要件

#### R1: 共通計測ユーティリティの作成

1. `internal/stats/` パッケージに、汎用的な実行時間計測・蓄積ユーティリティを作成する
2. コマンド名（文字列）をキーとし、以下の情報を保持する:
   - 直近1秒間のコマンド実行回数
   - 直近1秒間の平均実行時間（ナノ秒）
   - 直近1秒間の最大実行時間（ナノ秒）
3. 1秒間ウィンドウの区切りは wall clock ベースとする（`time.Now()` 使用）
4. ウィンドウが切り替わるタイミングで、前ウィンドウの集計結果を「確定済みスナップショット」として保持し、現在ウィンドウの集計をリセットする
5. スレッドセーフであること（複数goroutineから安全に呼び出せる）
6. 将来的に計測単位（5秒平均、1分平均等）を追加拡張できる設計とする

#### R2: VRAMコマンド別パフォーマンス計測

1. VRAMモジュールの `handleMessage` 内で、各コマンド（`draw_pixel`, `clear_vram`, `blit_rect`, `blit_rect_transform`, `copy_rect`, `set_palette`, `set_palette_block` 等）の実行前後で経過時間を計測する
2. 計測結果を R1 の共通ユーティリティに記録する
3. パレットの読み取り操作（`read_rect`, `read_palette_block`）も計測対象に含める
4. 計測自体のオーバーヘッドは `time.Now()` 呼び出し2回分 + マップ操作程度に抑える

#### R3: VRAMスタット取得コマンド

1. バストピック `vram` に対し、ターゲット `get_stats` のコマンドを追加する
2. VRAM はこのコマンドを受信したら、確定済みスナップショット（直近1秒の統計）を `vram_update` トピックにターゲット `stats_data` として発行する
3. レスポンスデータフォーマット: JSON バイト列

```json
{
  "commands": {
    "draw_pixel":          { "count": 1200, "avg_ns": 450,   "max_ns": 1200 },
    "blit_rect":           { "count": 30,   "avg_ns": 85000, "max_ns": 120000 },
    "blit_rect_transform": { "count": 5,    "avg_ns": 250000,"max_ns": 400000 },
    "clear_vram":          { "count": 1,    "avg_ns": 50000, "max_ns": 50000 },
    "copy_rect":           { "count": 10,   "avg_ns": 30000, "max_ns": 45000 }
  }
}
```

4. VRAMModule に `GetStats() map[string]stats.CommandStat` エクスポートメソッドを追加し、外部から直接参照も可能とする

#### R4: Monitor FPS計測

1. Monitor モジュール内で、`onPaint` が呼ばれるたびにフレームカウントをインクリメントする
2. 1秒間ウィンドウごとのフレーム数を FPS として保持する
3. Headless モードでも計測ロジックは動作させる（`receiveLoop` 内のメッセージ処理回数をカウント）

#### R5: Monitor スタット取得コマンド

1. Monitor モジュールに `monitor` バストピックのサブスクリプションを追加する
2. ターゲット `get_stats` のコマンドを受信したら、FPS 情報を `monitor_update` トピックにターゲット `stats_data` として発行する
3. レスポンスデータフォーマット: JSON バイト列

```json
{
  "fps": 60.0,
  "frames_last_sec": 60
}
```

4. MonitorModule に `GetFPS() float64` エクスポートメソッドを追加する

#### R6: HTTP スタットエンドポイント

1. `--stats-port` 起動オプションを追加する（デフォルト: 空文字 = 無効）
2. 指定された場合、HTTP サーバーを起動し以下のエンドポイントを提供する:
   - `GET /stats` → VRAM + Monitor のスタット一括取得（JSON）
   - `GET /stats/vram` → VRAMスタットのみ
   - `GET /stats/monitor` → Monitorスタットのみ
3. HTTP サーバーはシャットダウン時に `Shutdown(ctx)` で適切に停止する
4. VRAM/Monitor モジュールのエクスポートメソッド（`GetStats()`, `GetFPS()`）を直接呼び出して取得する（バス経由ではない）

レスポンス例（`GET /stats`）:

```json
{
  "vram": {
    "commands": {
      "draw_pixel":  { "count": 1200, "avg_ns": 450,   "max_ns": 1200 },
      "blit_rect":   { "count": 30,   "avg_ns": 85000, "max_ns": 120000 }
    }
  },
  "monitor": {
    "fps": 60.0,
    "frames_last_sec": 60
  }
}
```

#### R7: Stats CLI (`features/stats`)

1. `features/stats/main.go` に、HTTP エンドポイントからスタットを取得して表示する CLI を作成する
2. 起動オプション:
   - `--endpoint` (デフォルト: `http://localhost:8080/stats`)
   - `--watch` (デフォルト: `false`) ... `true` の場合、1秒間隔で繰り返し取得・表示する
3. 表示フォーマット: テーブル形式（ターミナル出力）

```
=== VRAM Stats (1s avg) ===
Command              Count    Avg(μs)   Max(μs)
─────────────────────────────────────────────────
draw_pixel            1200      0.45      1.20
blit_rect               30     85.00    120.00
blit_rect_transform       5    250.00    400.00
clear_vram                1     50.00     50.00
copy_rect                10     30.00     45.00

=== Monitor Stats ===
FPS: 60.0
```

4. `--watch` 時はターミナルをクリアして上書き表示（`\033[H\033[2J` ANSI エスケープ）する

### 任意要件

- 計測ウィンドウを複数持つ拡張（5秒平均、1分平均等）は将来の拡張として設計のみ考慮する
- Prometheus メトリクス形式 (`/metrics`) のエクスポートは将来の検討事項とする

## 実現方針 (Implementation Approach)

### パッケージ構成

```
features/neurom/
├── internal/
│   ├── stats/
│   │   └── stats.go          ← 共通計測ユーティリティ (新規)
│   ├── modules/
│   │   ├── vram/
│   │   │   └── vram.go       ← 計測コード追加
│   │   └── monitor/
│   │       └── monitor.go    ← FPS計測 + stats購読追加
│   └── statsserver/
│       └── server.go         ← HTTP サーバー (新規)
└── cmd/
    └── main.go               ← --stats-port フラグ追加

features/stats/
├── go.mod
└── main.go                   ← CLI実装
```

### 共通計測ユーティリティ設計

```go
package stats

type CommandStat struct {
    Count  int64
    AvgNs  int64
    MaxNs  int64
}

type Collector struct {
    mu        sync.Mutex
    current   map[string]*windowAccum  // 現在ウィンドウの蓄積
    snapshot  map[string]CommandStat   // 確定済みスナップショット
    windowEnd time.Time               // 現在ウィンドウの終了時刻
}

// Record records a single command execution duration.
func (c *Collector) Record(command string, d time.Duration)

// Snapshot returns the last completed window's stats.
func (c *Collector) Snapshot() map[string]CommandStat
```

`Record` が呼ばれるたびに、現在時刻がウィンドウ終了時刻を超えていれば:
1. `current` を `snapshot` にコピー（平均計算含む）
2. `current` をリセット
3. `windowEnd` を次の1秒後に更新

### VRAMへの組み込み

```go
func (v *VRAMModule) handleMessage(msg *bus.BusMessage) {
    if msg.Operation != bus.OpCommand {
        return
    }

    if msg.Target == "get_stats" {
        v.publishStats()
        return
    }

    start := time.Now()
    defer func() {
        v.stats.Record(msg.Target, time.Since(start))
    }()

    v.mu.Lock()
    defer v.mu.Unlock()

    switch msg.Target {
    // ... existing handlers ...
    }
}
```

注意: `get_stats` は `mu.Lock()` の外で処理する。`stats.Collector` は自身のミューテックスで保護される。

### Monitor FPS 計測

```go
func (m *MonitorModule) onPaint(glctx gl.Context) {
    m.fpsCounter.Record("paint", 0) // duration=0: count only
    // ... existing paint code ...
}
```

FPS は `Snapshot()["paint"].Count` で直近1秒のフレーム数 = FPS として取得する。

### HTTP スタットサーバー

```go
type StatsServer struct {
    vram    VRAMStatsProvider
    monitor MonitorStatsProvider
    server  *http.Server
}

type VRAMStatsProvider interface {
    GetStats() map[string]stats.CommandStat
}

type MonitorStatsProvider interface {
    GetFPS() float64
    GetFrameCount() int64
}
```

インターフェースを使用することで、テスト時にモック差し替えが可能。

### Stats CLI 通信フロー

```
features/stats CLI
    │
    ├── HTTP GET http://localhost:8080/stats
    │
    ↓
neurom StatsServer (net/http)
    │
    ├── vramMod.GetStats()   → map[string]CommandStat
    ├── monMod.GetFPS()      → float64
    │
    └── JSON response
```

- 外部依存は標準ライブラリ (`net/http`, `encoding/json`) のみ
- ZMQ 不要のため `features/stats/go.mod` に外部依存追加なし

## 検証シナリオ (Verification Scenarios)

### シナリオ1: 共通計測ユーティリティの動作確認

1. `stats.Collector` を生成する
2. コマンド "test_cmd" を 100 回、各 1ms の Duration で `Record` する
3. `Snapshot()` を呼び出す
4. "test_cmd" の Count が 100、AvgNs が約 1,000,000 (1ms) であることを確認する

### シナリオ2: ウィンドウ切り替え

1. `stats.Collector` を生成する
2. コマンド "a" を 10 回 Record する
3. 1秒以上待機する
4. コマンド "a" を 5 回 Record する
5. `Snapshot()` を呼び出す
6. 最初のウィンドウ（Count=10）の結果が確定スナップショットに入っていることを確認する

### シナリオ3: VRAM スタット取得

1. VRAMModule を初期化し起動する
2. `draw_pixel` コマンドを 50 回送信する
3. `blit_rect` コマンドを 10 回送信する
4. 1秒以上待機する（ウィンドウ確定のため）
5. `get_stats` コマンドをバス経由で送信する
6. `stats_data` レスポンスを受信し、JSON をパースする
7. `draw_pixel` と `blit_rect` のエントリが存在し、Count が期待値であることを確認する

### シナリオ4: Monitor FPS 取得

1. MonitorModule を headless モードで初期化・起動する
2. `vram_updated` イベントを 60 回/秒のペースで 2 秒間送信する
3. `get_stats` コマンドを `monitor` トピックに送信する
4. `stats_data` レスポンスを受信する
5. FPS 値が概ね 60 付近であることを確認する

### シナリオ5: HTTP エンドポイント

1. neurom を `--stats-port 18080` で起動する (テスト用ポート)
2. いくつかの VRAM コマンドを送信する
3. `GET http://localhost:18080/stats` にリクエストを送る
4. JSON レスポンスに `vram` と `monitor` のキーが含まれることを確認する
5. `vram.commands` に送信したコマンドのエントリが存在することを確認する

### シナリオ6: Stats CLI

1. neurom を `--stats-port 18080` で起動する
2. いくつかの VRAM コマンドを送信する
3. `features/stats` CLI を `--endpoint http://localhost:18080/stats` で実行する
4. テーブル形式の出力が表示されることを確認する
5. 表示されたコマンド名と Count が期待値と一致することを確認する

### シナリオ7: 計測オーバーヘッド

1. 計測有効状態で `blit_rect` (100x100) を 1000 回実行し、所要時間を記録する
2. 計測のオーバーヘッドが全体の 1% 未満であることを確認する（`time.Now()` 2回の呼び出しは通常 ~100ns）

## テスト項目 (Testing for the Requirements)

### 単体テスト

| テスト | 対象要件 | 検証方法 |
|-------|---------|---------|
| `TestCollectorRecord` | R1 | 複数コマンドを Record し Snapshot で正しい値が返ることを確認 |
| `TestCollectorWindowRotation` | R1 | 1秒経過後のウィンドウ切り替えで前ウィンドウが確定されることを確認 |
| `TestCollectorConcurrency` | R1 | 複数 goroutine から同時 Record してデータ競合がないことを確認（`-race`） |
| `TestCollectorMaxNs` | R1 | 最大実行時間が正しく記録されることを確認 |
| `TestVRAMStatsRecording` | R2 | 各コマンド実行後に stats.Collector に記録されていることを確認 |
| `TestVRAMGetStats` | R3 | `get_stats` コマンドで `stats_data` イベントが発行されることを確認 |
| `TestMonitorFPSCounter` | R4 | `onPaint` 相当の操作後に FPS が正しくカウントされることを確認 |
| `TestMonitorGetStats` | R5 | `get_stats` コマンドで FPS データが返ることを確認 |
| `TestStatsServerEndpoints` | R6 | HTTP `/stats`, `/stats/vram`, `/stats/monitor` の各エンドポイントのレスポンスを確認 |

### 統合テスト

| テスト | 対象要件 | 検証方法 |
|-------|---------|---------|
| `TestVRAMStatsIntegration` | R2, R3 | フル起動 → VRAM コマンド送信 → バス経由 stats 取得 → 値検証 |
| `TestMonitorStatsIntegration` | R4, R5 | フル起動 → vram_update イベント送信 → バス経由 FPS 取得 → 値検証 |
| `TestHTTPStatsIntegration` | R6 | フル起動 + HTTP → HTTP GET → JSON検証 |

### 検証コマンド

```bash
# 全体ビルドと単体テスト
scripts/process/build.sh

# 統合テスト
scripts/process/integration_test.sh

# stats 関連テストのみ
scripts/process/integration_test.sh --specify TestStats
scripts/process/integration_test.sh --specify TestVRAMStats
scripts/process/integration_test.sh --specify TestMonitorStats
```
