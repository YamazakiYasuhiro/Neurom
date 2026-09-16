# 003-ColorPaletteSystem

> **Source Specification**: prompts/phases/000-foundation/ideas/main/003-ColorPaletteSystem.md

## Goal Description
VDP（仮想ディスプレイ）に VRAM を拡張する形で 256色の Color RAM (カラーパレット) の概念を実装し、Monitor モジュールが固定計算ではなく動的なパレットマップを用いてピクセルの色を柔軟に表現（アニメーションなど）できるようにする。

## User Review Required
パレットの初期状態について: 起動直後の画面が真黒にならないよう、Monitor起動時の初期パレットは `p=255` を白とし、既存の実装である "(p>>5)&7" 風の計算を初期設定として組み込んでおきます。これで問題ないかご確認ください。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| パレット（Color RAM）の導入 | `VRAMModule` における `palette` 配列の追加 |
| 動的な色指定機能 (`set_palette`) | `VRAMModule` の `handleMessage` に `set_palette` ケースを追加 |
| モニターの描画連動 | `MonitorModule` の描画ループで `m.palette[p]` 参照へ変更 |
| パレット更新の伝播 | `VRAMModule` パブリッシュ と `MonitorModule` 購読・反映 |

## Proposed Changes

### VRAM Module

#### [MODIFY] features/vm/internal/modules/vram/vram.go
*   **Description**: VRAM モジュールにパレット用配列を持たせ、バスからの書き込みイベントを受領可能にする。
*   **Technical Design**:
    *   ```go
        type VRAMModule struct {
            mu      sync.RWMutex
            width   int
            height  int
            color   int
            buffer  []uint8
            palette [256][3]uint8 // 新規追加: [Index][R, G, B]
            bus     bus.Bus
        }
        ```
    *   `New()` 関数で `palette` フィールドのゼロ値初期化（Go言語仕様で初期化されるが、明示化を推奨）
*   **Logic**:
    *   `handleMessage` に `case "set_palette":` を追加。
    *   `msg.Data` は長さ 4 バイト `[index, R, G, B]` が期待される。
    *   条件を満たした場合、`v.palette[index] = [3]uint8{R, G, B}` へ代入する。
    *   代入後、システムバスに対して `Target: "palette_updated", Operation: bus.OpCommand, Data: msg.Data, Source: v.Name()` をパブリッシュする（Monitorへ通知するため）。

### Monitor Module

#### [MODIFY] features/vm/internal/modules/monitor/monitor.go
*   **Description**: Monitorモジュールがパレットのコピーを持ち、ピクセル描画にそのパレット値を参照する。バスからのアップデートを受け取ってパレットを更新する。
*   **Technical Design**:
    *   ```go
        type MonitorModule struct {
            // ... 既存のフィールド
            palette [256][3]uint8 // パレットのローカルキャッシュ
        }
        ```
    *   `New()` 関数内で、`palette` を初期設定するループを設ける。
        ※初期設定のロジック: 従来の固定シフト計算 `[(p>>5)&7...]` と同等の内容を、起動時にパレット0〜255へ書き込んでおく。これにより後方互換描画が保たれる。
*   **Logic**:
    *   `receiveLoop` (または `handleMessage`) で、もし `msg.Target == "palette_updated"` であれば、ローカルの `m.palette[index] = [3]uint8{R, G, B}` を更新する。
    *   `vram_updated` の処理部分（すでに `r, g, b` をマッピングしている箇所）において、計算式ではなく以下のように変更する:
        ```go
        r, g, b := m.palette[p][0], m.palette[p][1], m.palette[p][2]
        ```

### Integration Tests

#### [NEW] features/vm/integration/palette_test.go
*   **Description**: パレット更新とモニターのVBOへの反映テスト。
*   **Logic**:
    *   バスを起動し VRAM と Monitor(headless) を繋ぐ。
    *   バスへ `set_palette` メッセージ (`index: 0x01`, `R:255, G:0, B:0`) をパブリッシュ。
    *   少し待機後、`draw_pixel` メッセージ (`X:10, Y:10`, `p:0x01`) をパブリッシュ。
    *   Monitor モジュールのテクスチャバッファを取得し、座標 `(10, 10)` のバイト配列が `(255, 0, 0, 255)` に書き換わっていることをアサートする。

## Step-by-Step Implementation Guide

1.  **[x] Step 1**:
    *   Edit `features/vm/internal/modules/vram/vram.go`.
    *   `VRAMModule` 構造体に `palette [256][3]uint8` を追加。
    *   `handleMessage` メソッドに `set_palette` 処理とバスパブリッシュを追加。
2.  **[x] Step 2**:
    *   Edit `features/vm/internal/modules/monitor/monitor.go`.
    *   構造体に `palette` キャッシュを用意し、`New()` で従来の固定シフト計算を用いたデフォルト色へ初期化する。
    *   `palette_updated` メッセージを処理してキャッシュを更新するロジックを追加。
    *   `receiveLoop` 内のピクセルマッピング処理を `m.palette[p]` 参照へ書き換える。
3.  **[x] Step 3**:
    *   Create `features/vm/integration/palette_test.go`.
    *   動的パレット変更の統合テストを記述。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    run the build script.
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests**:
    Run integration tests.
    ```bash
    ./scripts/process/integration_test.sh --specify "TestPaletteUpdate"
    ```
    *   **Log Verification**: 仮想バス経由で色が変わっていることがテストで出力されて完了（PASS）となること。

## Documentation

*更新対象の仕様ドキュメントはありません。*
