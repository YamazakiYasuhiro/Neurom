# Features

This directory contains individual features of the project.

Each feature is an independent module with its own codebase, 
tests, and configuration.

## Adding a Feature

Use `devctl scaffold features <template>` to generate a new feature structure.

## Directory Convention

```
features/
  <feature-name>/
    cmd/           # CLI entry points
    internal/      # Internal packages
    go.mod         # Go module definition
```

## Available Features

### `vm`
Neurom は独立したハードウェアモジュールが仮想システムバス (ZeroMQ) を通じて通信する、ARC Architecture 準拠のエミュレータシステム基盤です。付属のサンプルプログラムとして「Metov」などのゲームを稼働させることができます。

**主なモジュール:**
- `cpu`: メインプログラムや各種デバイスへのコマンド発行
- `vram`: ピクセル操作コマンドやパレット変更を受信・管理
- `monitor`: VRAMのイベントを受信し、実際のウィンドウへの描画を担当
- `bus`: ZeroMQ (`go-zmq4`) ベースのPub/Subシステムバス

**実行方法:**
```bash
# ビルド
./scripts/process/build.sh

# 実行
cd features/vm
go run ./cmd/main.go [--headless] [--tcp-port 5555] [--stats-port 8080] [--cpu N] [--vram-strategy static|dynamic]
```

### `stats`
HTTP エンドポイント経由で neurom のパフォーマンス統計情報を取得・表示する CLI ツールです。

**前提:** neurom を `--stats-port` 付きで起動する必要があります。

**使用方法:**
```bash
cd features/stats

# 1回取得して表示
go run . --endpoint http://localhost:8080/stats

# 1秒ごとに自動更新
go run . --endpoint http://localhost:8080/stats --watch
```

**オプション:**
- `--endpoint`: Stats HTTP エンドポイント URL（デフォルト: `http://localhost:8080/stats`）
- `--watch`: 1秒ごとにターミナルをクリアして統計情報を更新表示

**表示例:**
```
=== VRAM Stats ===
Command                  ──── 1s ────   ──── 10s ────   ──── 30s ────
                         Count Avg(μs)  Count Avg(μs)   Count Avg(μs)
──────────────────────────────────────────────────────────────────────
draw_pixel                  10    5.00      95    4.80     290    4.90
set_palette                  0    0.00       5    6.20      15    6.10

=== Monitor Stats ===
FPS (1s): 60.0   FPS (10s): 58.5   FPS (30s): 57.2
```

**HTTP レスポンス例 (`GET /stats`):**
```json
{
  "vram": {
    "commands": {
      "draw_pixel": {
        "last_1s":  { "count": 10, "avg_ns": 5000, "max_ns": 12000 },
        "last_10s": { "count": 95, "avg_ns": 4800, "max_ns": 15000 },
        "last_30s": { "count": 290, "avg_ns": 4900, "max_ns": 15000 }
      }
    }
  },
  "monitor": {
    "fps_1s": 60.0,
    "fps_10s": 58.5,
    "fps_30s": 57.2
  }
}
```
