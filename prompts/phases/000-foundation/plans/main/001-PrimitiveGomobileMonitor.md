# 001-PrimitiveGomobileMonitor

> **Source Specification**: prompts/phases/000-foundation/ideas/main/001-PrimitiveGomobileMonitor.md

## Goal Description
`golang.org/x/mobile/app` および `golang.org/x/mobile/gl` パッケージを使用し、ゲームエンジン等の不要な依存なしに、OpenGL ESを用いたプリミティブなディスプレイモニタの描画ロジックを実装する。また、OSのUIスレッド制約を遵守できるよう VMの起動アーキテクチャを改修する。

## User Review Required
None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R-001: 描画レイヤーの直接操作 (ゲームライブラリ不使用) | `Proposed Changes > features/vm/internal/modules/monitor` |
| R-002: モバイルフレームワークの使用 (`golang.org/x/mobile/app`) | `Proposed Changes > features/vm/internal/modules/monitor/monitor.go` |
| R-003: OpenGL ES での VRAM 描画 (Quad, Texture) | `Proposed Changes > features/vm/internal/modules/monitor/monitor.go` |
| R-004: メインスレッド要件の解決 (`app.Main` への処理委譲) | `Proposed Changes > features/vm/cmd/main.go` |
| R-005: Headlessモード対応の維持 | `Proposed Changes > features/vm/internal/modules/monitor/monitor.go` (if headless bypass) |

## Proposed Changes

### Bus (Message Update)

#### [MODIFY] features/vm/internal/modules/vram/vram.go
*   **Description**: VRAMモジュールが `vram_updated` 時に全ての情報を送るか、効率の良い描画要求を送るように補強
*   **Logic**:
    *   現在 `DrawPixel` 時に単一のピクセル更新のみを Monitor に送っているが、Monitor が自身の RGBA テクスチャを更新できるようにしていることは変わらない。
    *   パレット管理の概念が存在するため、初期パレットのRGB値を Monitor 側が固定で持つか、VRAM に持たせるか。今回は Monitor 側に簡易的な 8bitパレット->RGB 変換テーブルを持たせる設計とする。

### Monitor Module

#### [MODIFY] features/vm/internal/modules/monitor/monitor.go
*   **Description**: モニタモジュールに OpenGL 描画用イベントループとシェーダロジックを追加。
*   **Technical Design**:
    *   以下のGLSLシェーダを使用する。
        ```glsl
        // Vertex Shader
        attribute vec4 position;
        attribute vec2 texcoord;
        varying vec2 v_texcoord;
        void main() {
            gl_Position = position;
            v_texcoord = texcoord;
        }

        // Fragment Shader
        precision mediump float;
        varying vec2 v_texcoord;
        uniform sampler2D tex;
        void main() {
            gl_FragColor = texture2D(tex, v_texcoord);
        }
        ```
    *   追加する構造体フィールド
        ```go
        type MonitorModule struct {
            config MonitorConfig
            bus    bus.Bus
            rgba   []byte // len = 256*212*4
            width  int
            height int
            tex    gl.Texture
            prog   gl.Program
            app    app.App
            // ...
        }
        ```
    *   `RunMain()` メソッドを追加し、Headless でない場合のみ `app.Main(m.runApp)` をメインスレッドで実行する。
*   **Logic**:
    *   `run()` 内の従来の Headless==false 処理を削除し、新たに `RunMain()` に移譲する。
    *   `app.Main` コールバック内では、`lifecycle.Event` (Cross -> AliveでGL初期化)、`size.Event` (ビューポート設定)、`paint.Event` (GL描画)、を取りまとめる。
    *   Busメッセージを受信した際 (ゴルーチン内) は `m.rgba` バッファを更新し、`m.app.Publish()` あるいは同期チャネルで画面更新イベントを発火させる。
    *   `paint.Event` 時:
        1. `gl.Clear`
        2. `gl.UseProgram`
        3. `gl.BindTexture`
        4. `gl.TexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, width, height, gl.RGBA, gl.UNSIGNED_BYTE, m.rgba)` でテクスチャ転送
        5. VBOをバインドし `gl.DrawArrays(gl.TRIANGLES, 0, 6)` を実行。
    *   パレット変換: 8bitカラー (例: `p`) を `[R, G, B, 0xFF]` に変換。（R = p>>5, G = (p>>2)&7, B = p&3 のような単純なシフト・論理積で暫定マッピングする）

### Entrypoint

#### [MODIFY] features/vm/cmd/main.go
*   **Description**: Ebitengine等のGUIアプリと同じく、UIイベントループをメインスレッドで占有するアーキテクチャへの改修。
*   **Technical Design**:
    *   現在のように `<-sigCh` で待機してVMを終了させる処理を、Headless の場合はそのまま残すが、Monitor がある場合は `monitor.RunMain()` を呼び出しブロッキングさせる。
*   **Logic**:
    *   ```go
        mon := monitor.New(...)
        mgr.Register(mon)
        if err := mgr.StartAll(ctx); err != nil { ... }
        
        if *headless {
            // signal.Notify(...) wait
        } else {
            // monitor takes over main thread
            mon.RunMain()
        }
        mgr.StopAll()
        ```

## Step-by-Step Implementation Guide

1.  **[x] Step 1: Update main.go constraints**:
    *   Edit `features/vm/cmd/main.go` to route execution into `mon.RunMain()` when not running headless.
2.  **[x] Step 2: Implement OpenGL Shaders and Texturing in Monitor**:
    *   Edit `features/vm/internal/modules/monitor/monitor.go` to add GLSL shaders string, vertex array coordinates, and `RunMain` method implementing `app.Main`.
3.  **[x] Step 3: Map VRAM Events to RGBA Texture**:
    *   Edit `features/vm/internal/modules/monitor/monitor.go` to parse `vram_updated` bus messages, apply a basic RGB mapping, store into `m.rgba` array, and mark as dirty/publish to trigger `paint.Event`.
4.  **[x] Step 4: Validate Headless Mode integration**:
    *   Ensure that `--headless=true` completely bypasses `app.Main` and the existing integration tests continue to pass.

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    run the build script.
    ```bash
    ./scripts/process/build.sh
    ```
    *Note: CGO_ENABLED=1 might be implicitly required by x/mobile on some OS, but `build.sh` run will quickly confirm basic syntactic/linkage correctness.*

2.  **Integration Tests**:
    Run integration tests.
    ```bash
    ./scripts/process/integration_test.sh --specify "integration"
    ```
    *   **Log Verification**: 結合テストが `HEADLESS` 前提で正しく完走し、デッドロックを起こさないか確認。

### Manual Verification
実行コマンド：
```bash
cd features/vm && go mod tidy && go run ./cmd/main.go
```
ウィンドウが立ち上がり、VRAMのダミー描画によって色が変化する四角形が画面上に視認できるか確認する。

## Documentation

#### [MODIFY] features/README.md
*   **更新内容**: 実装された OpenGL モニターの起動要件や、`x/mobile` についてのアーキテクチャメモを記載する。
