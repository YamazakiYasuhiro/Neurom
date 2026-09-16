# 023: ZMQBus パニック問題の根本解決

## 背景 (Background)

### 現状の問題

Neurom VM の統合テストにおいて、`go-zeromq/zmq4` v0.17.0 ライブラリの **inproc トランスポート** で断続的にパニックが発生している。

```
panic: runtime error: makeslice: len out of range

goroutine 84 [running]:
github.com/go-zeromq/zmq4.(*Conn).read(0xc000322000)
    .../zmq4@v0.17.0/conn.go:416 +0x1af
github.com/go-zeromq/zmq4.(*qreader).listen(...)
    .../zmq4@v0.17.0/msgio.go:105 +0xae
```

このパニックは zmq4 ライブラリ内部の ZMTP フレーム解析処理に起因し、inproc トランスポートで複数回のソケット接続・切断を繰り返すと、不整合なフレームヘッダが読み込まれることで `makeslice: len out of range` が発生する。これはアプリケーション側のコードでは防ぎようがない、ライブラリレベルのバグである。

### これまでの対応

- **ChannelBus の導入** (`channelbus.go`): zmq4 を一切使用しない純粋な Go チャネルベースのメッセージバスを実装済み。プロセス内通信ではこちらが安定動作する。
- **TCPBridge の導入** (`tcpbridge.go`): 外部接続が必要な場合のみ ZMQ TCP トランスポートを使用し、パニックリカバリ付きで隔離する仕組みを実装済み。
- **大半のテストは移行済み**: `bus_panic_guard_test.go`, `shutdown_test.go`, `vram_enhancement_test.go`, `vram_multicore_test.go`, `vram_page_test.go`, `vram_stats_test.go`, `stats_http_test.go` はすべて `ChannelBus` を使用中。

### 残存する問題

以下の **2つの統合テスト** がまだ `bus.NewZMQBus()` を直接使用しており、テスト実行時に断続的なパニックを引き起こしている：

| テスト名 | ファイル | 問題箇所 |
|---|---|---|
| `TestVRAMMonitorIntegration` | `integration/vram_monitor_test.go:16` | `bus.NewZMQBus("inproc://test-integration-bus")` |
| `TestPaletteUpdate` | `integration/palette_test.go:15` | `bus.NewZMQBus("inproc://test-palette-bus")` |

## 要件 (Requirements)

### 必須要件

1. **R1: ZMQBus 直接利用の排除**
   - `TestVRAMMonitorIntegration` と `TestPaletteUpdate` で使用している `bus.NewZMQBus()` を `bus.NewChannelBus()` に置き換える。
   - ZMQ ソケットの `Listen`/`Dial`/`Close` 処理や `time.Sleep` による接続待機が不要になるため、関連コードも削除・簡素化する。

2. **R2: テストの機能的な正当性の維持**
   - 置き換え後もテストのアサーション・検証ロジックは変更しない。
   - VRAM → Monitor 間のメッセージパッシングとパレット更新のE2E検証が引き続き正しく動作すること。

3. **R3: パニックフリーの確認**
   - 修正後、統合テストを `-count=5` 以上で繰り返し実行し、パニックが再発しないことを確認する。

### 任意要件

4. **R4: ZMQBus 単体テストの存続確認**
   - `internal/bus/zmqbus_test.go` の `TestZMQBus` は、ZMQBus 自体の基本動作テストとして残す（TCPBridge が ZMQBus に依存するため）。ただし、統合テストから ZMQ 依存を排除することが目的であり、このテストの変更は不要。

## 実現方針 (Implementation Approach)

### 変更対象ファイル

#### 1. `features/neurom/integration/vram_monitor_test.go`

- `bus.NewZMQBus("inproc://test-integration-bus")` → `bus.NewChannelBus()` に変更
- `defer b.Close()` はそのまま維持（`ChannelBus` も `Close()` を実装）
- ZMQBus 固有のエラーハンドリング（`if err != nil` ブロック）は `NewChannelBus()` がエラーを返さないため削除

```go
// Before
b, err := bus.NewZMQBus("inproc://test-integration-bus")
if err != nil {
    t.Fatalf("Failed to init bus: %v", err)
}
defer b.Close()

// After
b := bus.NewChannelBus()
defer b.Close()
```

#### 2. `features/neurom/integration/palette_test.go`

- 同様に `bus.NewZMQBus("inproc://test-palette-bus")` → `bus.NewChannelBus()` に変更
- エラーハンドリングの削除
- `time.Sleep(100 * time.Millisecond)` （ZMQ サブスクリプション伝搬待ち）は `ChannelBus` では即時反映されるため不要だが、安全のため残しても良い

```go
// Before
b, err := bus.NewZMQBus("inproc://test-palette-bus")
if err != nil {
    t.Fatalf("Failed to init bus: %v", err)
}
defer b.Close()

// After
b := bus.NewChannelBus()
defer b.Close()
```

### 変更しないファイル

- `internal/bus/zmqbus.go` — TCPBridge が依存するため変更不要
- `internal/bus/zmqbus_test.go` — ZMQBus 自体の単体テストとして存続
- `internal/bus/tcpbridge.go` — 外部接続ブリッジとして独立して動作
- その他すべての統合テスト — すでに `ChannelBus` を使用中

## 検証シナリオ (Verification Scenarios)

1. `vram_monitor_test.go` で `NewZMQBus` を `NewChannelBus` に変更する
2. `palette_test.go` で `NewZMQBus` を `NewChannelBus` に変更する
3. 単体テスト (`go test ./internal/bus/...`) を実行し、ZMQBus 単体テストが影響を受けていないことを確認する
4. 統合テストを `-count=1` で実行し、全テストが PASS することを確認する
5. 統合テストを `-count=5` で繰り返し実行し、パニックが再発しないことを確認する
6. `build.sh` でプロジェクト全体のビルドと単体テストを実行し、リグレッションがないことを確認する

## テスト項目 (Testing for the Requirements)

| 要件 | 検証方法 | コマンド |
|---|---|---|
| R1: ZMQBus 直接利用の排除 | ソースコード上で `integration/` ディレクトリ内の `NewZMQBus` 呼び出しが0件であることを確認 | `grep -r "NewZMQBus" features/neurom/integration/` |
| R2: テスト機能的正当性 | 統合テスト全件 PASS | `scripts/process/integration_test.sh` (`go test -v -count=1 ./integration/...`) |
| R3: パニックフリー確認 | 統合テスト5回繰り返し実行でパニック0件 | `cd features/neurom && go test -v -count=5 -timeout 300s ./integration/...` |
| R4: ZMQBus 単体テスト存続 | ZMQBus の単体テストが PASS | `cd features/neurom && go test -v ./internal/bus/...` |
| 全体リグレッション | ビルド + 全体テスト | `scripts/process/build.sh` |

## 対応ステータス

- **ステータス**: ✅ 対応完了 (2026-04-09)
- **実装計画**: [023-ZMQBusPanicElimination.md](file://prompts/phases/000-foundation/plans/main/023-ZMQBusPanicElimination.md)
- **検証結果**:
  - `build.sh`: PASS
  - 統合テスト (`-count=1`): 全件 PASS
  - パニックフリー確認 (`-count=5`): 全件 PASS、パニック 0 件
  - `grep -r "NewZMQBus" integration/`: 0 件（完全排除）

