# 004-FrameRenderingPipeline

## 1. 背景 (Background)
現在の Monitor モジュールは、バスから `draw_pixel` や `palette_updated` メッセージを受信した**直後に即時**ピクセルのRGB変換を行い、OpenGL 用の `rgba` バッファを書き換えています。
しかし、実際のVDP（ビデオディスプレイプロセッサ）やエミュレータは、VRAM（システムの状態）とパレットを別々に保持しておき、**画面描画（VSYNC）のタイミングで VRAM とパレットを合成して1フレームを作り出す** という仕組みが一般的です。
今後の BG（背景レイヤー）やスプライト合成といった拡張に備え、「状態の保持」と「フレームの描画」フェーズを分離する真のレンダリングパイプラインを実装します。

## 2. 要件 (Requirements)
*   **VRAMと描画の分離**: 
    *   バスメッセージによる更新イベントでは、内部の `vram` キャッシュあるいは `palette` キャッシュのみを更新し、**RGBの再計算を行わない**。
*   **フレーム一括合成**:
    *   画面が更新を要する状態（`dirty == true`）のとき、描画処理の直前に `vram` キャッシュを走査してパレットを割り当て、1フレーム分の `rgba` アレイを一括生成する処理（フレーム生成フェーズ）を導入する。
*   **Headless モードへの配慮**:
    *   UI非表示（テスト環境）であっても、合成された `rgba` のピクセルテストが行えるよう、フレーム生成処理は UI フレームワークから独立した形（専用メソッド `buildFrame()` など）にする。

## 3. 実現方針 (Implementation Approach)
1.  **Monitorモジュールの `receiveLoop` の単純化**:
    *   `palette_updated` 処理から、`vram` を全走査して `rgba` を上書く 5万回のループを削除する。単にパレット配列を更新するのみとする。
    *   `vram_updated` 処理でも、即座にRGBマッピングを行うコードを削除し、`m.vram[y*width+x] = p` のみとする。
    *   状態が変化したため `m.dirty = true` フラグを立て、画面への再描画（`paint.Event` 送信）を要求する。
2.  **フレーム生成ロジックの追加 (`buildFrame`)**:
    *   `func (m *MonitorModule) buildFrame()` メソッドを新設する。
    *   `m.dirty` が true の場合、`vram` 配列（256x212）をループし、`rgba` バッファに `m.palette[ vram[i] ]` のRGB値を書き込む。完了後 `m.dirty = false` とする。
3.  **描画プロセス (`onPaint`) の改修**:
    *   `onPaint` の最初（またはテクスチャ更新の直前）で `buildFrame()` を呼び出し、確実に最新状態の `rgba` バッファを用意してから `TexImage2D` へ転送する流れとする。
4.  **`GetPixel` の改修**:
    *   統合テスト用に提供されている `GetPixel(x, y)` では、データ取得前に `buildFrame()` を呼び出すことで、Headless モードでも合成済みの最新色をテストできるようにする。

## 4. 検証シナリオ (Verification Scenarios)
1.  システムバスを起動し、Monitor モジュールと連携状態にする。
2.  CPUモジュールが実行するアニメーションテスト（`draw_pixel` 後に `set_palette` が連続で送られる）を走らせる。
3.  Monitor の `GetPixel` が、パレット変更メッセージの後ただちに（RGBの即時変換ループに頼ることなく）正しい更新後のRGBカラーを返すか統合テストで確認する。
4.  （GUIモード時）前フェーズと同等に動的なカラーサイクリング（アニメーション）が、カクつきなく滑らかに表示されることを目視確認する。

## 5. テスト項目 (Testing for the Requirements)
*   **Unit Tests (`internal/modules/monitor`)**:
    *   描画イベントが起きた際に、`buildFrame()` を通じて初めて `rgba` 配列が構成されることの単体動作チェック。
*   **Integration Tests (`integration/palette_test.go`)**:
    *   既存の `TestPaletteUpdate` をそのまま再利用する。
    *   内部の遅延合成アーキテクチャに変わっても、テストが既存通り通過することを確認する。
    *   実行コマンド: `./scripts/process/build.sh` および `./scripts/process/integration_test.sh`
