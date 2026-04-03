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
Metov VM は独立したハードウェアモジュールが仮想システムバス (ZeroMQ) を通じて通信する、レトロゲーム機風のソフトウェアアーキテクチャ基盤です。

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
go run ./cmd/main.go [--headless] [--tcp-port 5555]
```
