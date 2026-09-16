# 003-ColorPaletteSystem

## 1. 背景 (Background)
現在（002時点）のシステムでは、VDP（仮想ディスプレイ機能）は VRAM 上に書き込まれた 8-bit のデータ（`p`）を、固定のシフト演算によって直接 RGB に変換して画面に描画しています。
しかし、実際のレトロアーキテクチャ（ファミコンやMSX、GBAなど）では、グラフィックチップ（VDP等）内に **パレットメモリ（CRAM）** が存在し、画素データ `p` はそのパレットのインデックスとして機能します。
この概念が抜けていたため、プログラム（CPU側）から動的に「1番の色を赤にする」「2番の色を青にする」といったパレットアニメーションや柔軟な色指定を行える仕組みが必要となります。

## 2. 要件 (Requirements)
*   **パレット（Color RAM）の導入**: 256色（インデックス 0〜255）のカラーパレットを VRAM モジュール内に保持する。
*   **動的な色指定機能（API）**: システムバス経由で、特定インデックスの RGB 値を変更する機能（`set_palette` コマンド）を追加する。
*   **モニターの描画連動**: モニター（Monitor モジュール）は固定の計算による色決定をやめ、パレットメモリの内容を参照して実際の RGB 描画を行う。

## 3. 実現方針 (Implementation Approach)
本システムはシステムバスを中心とした疎結合アーキテクチャであるため、以下のように役割を整理します。

1.  **VRAMモジュールの拡張 (`vram.go`)**:
    *   内部にパレット配列 `palette [256][3]uint8` を持つ。
    *   `BusMessage` の Target として `"set_palette"` を追加。Data形式は `[index(1byte)][R(1byte)][G(1byte)][B(1byte)]` とする。
    *   パレットの更新を受信した際、自身を書き換えると共にバスへ `palette_updated` イベントをパブリッシュする。

2.  **Monitorモジュールの拡張 (`monitor.go`)**:
    *   内部キャッシュとして `palette [256][3]byte` を持つ（起動時は初期パレットとしてデフォルトの明暗やRGBパレット等を入れておくと親切）。
    *   バスから `palette_updated` イベントを購読（または `vram_update` のペイロード一部として）し、自身のキャッシュを最新に保つ。
    *   レンダー側はパレットマッピング `r, g, b = m.palette[p][0], m.palette[p][1], m.palette[p][2]` へと処理を書き換える。

## 4. 検証シナリオ (Verification Scenarios)
1. ゼロクリアされた VRAM パレットを持つシステムを立ち上げる。
2. バスを通じて `set_palette` コマンドを送信し、インデックス `0x01` 番を純粋な「赤 (255, 0, 0)」に設定する。
3. `draw_pixel` コマンドで、色インデックス `0x01` を使用して `(X:10, Y:10)` 座標へピクセルを描画する。
4. モニターモジュールが内部のテクスチャバッファ上で、該当座標を (255, 0, 0, 255) として保持していることを確認する。
5. （GUIモード時）画面上に明るい赤いドットが一発表示されることを確認する。

## 5. テスト項目 (Testing for the Requirements)
*   **Unit Tests (`internal/modules/vram`, `monitor`)**:
    *   `vram` モジュールが `set_palette` を受け取り正しく `palette_updated` を発出するか。
    *   `monitor` モジュールが `palette_updated` イベントを受信後、パレットの内部状態を更新できるか。
*   **Integration Tests (`integration/vram_monitor_test.go`)**:
    *   仮想 CPU/バス からパレットとピクセル描画メッセージを発行し、Monitor 内部の VBO バッファに想定通りの RGB バイトが書き込まれたかをアサート検証する。
    *   実行コマンド: `./scripts/process/integration_test.sh --specify "TestVRAMMonitorIntegration"`
