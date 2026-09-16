# 001-PrimitiveGomobileMonitor

## 1. 背景 (Background)
Neurom アーキテクチャの実装が進み、抽象化されたバス通信と各仮想モジュール間の基盤が完成しました。しかし、ディスプレイモニタ（Monitor モジュール）の描画部分について、ゲームエンジン（Ebitengineなど）のような余剰機能を含んだパッケージを使用せず、極めて原始的（Primitive）に自前で実装したいという要望があります。
この仕様書は、「`golang.org/x/mobile`（モバイルフレームワーク）を基軸とする」という初期仕様を厳格に満たしつつ、OpenGL ES等の低レイヤーAPIを叩いて VRAM の内容を直接ウィンドウに転送する機能の設計を定義します。

## 2. 要件 (Requirements)

**必須要件**:
1. **描画レイヤーの直接操作**: ゲームエンジンや高レベルなUIライブラリ・2Dグラフィックスライブラリ（`Ebitengine` など）は一切使用しない。
2. **モバイルフレームワークの使用**: `golang.org/x/mobile/app`, `golang.org/x/mobile/event/*`, `golang.org/x/mobile/gl` パッケージを使用する。
3. **OpenGL ES での VRAM 描画**:
   - VRAM ピクセルデータ（カラーパレット等を持つ）のデータを RGBA 配列等に展開する。
   - 画面全体を覆う単一の四角形（Quad）ポリゴンを用意する。
   - 上記のRGBA配列を2Dテクスチャに転送し、該当ポリゴンにテクスチャマッピングすることで画面出力を実現する。
4. **メインスレッド要件の解決**:
   - UIウィンドウシステムはOSの制約としてメインスレッド（`main`関数スレッド）を占有する必要があるため、`module.Manager` の実行モデルと並行して、正常にUIイベントループがメインスレッドで回るように `cmd/main.go` の構成を調整する。
5. **Headlessモード対応の維持**:
   - `--headless=true` が指定された場合は、UIシステムを初期化せず、従来通り通信ループだけが回る挙動を保つこと。

## 3. 実現方針 (Implementation Approach)

*   **メインスレッドとの連携**:
    *   Monitor モジュールに `RunWindowLoop()` といったエントリポイントメソッドを追加します。
    *   `cmd/main.go` で Headless ではない場合、全モジュール（ゴルーチン）をスタートした後の最後のステップとして `monitor.RunWindowLoop()` をメインスレッドでブロッキング実行します。
*   **OpenGL ES 描画ロジック**:
    *   `app.Main(func(a app.App) { ... })` 内でイベントループを構築します。
    *   `lifecycle.Event` 受信時の `lifecycle.StageGlContext` で、頂点シェーダ（Vertex Shader）およびフラグメントシェーダ（Fragment Shader）の GLSL をパース・コンパイルし、四角形描画用の VBO (Vertex Buffer Object) を初期化します。
    *   テクスチャ (`gl.TEXTURE_2D`) を確保します。
*   **VRAMからのピクセル転送**:
    *   Monitor モジュールは ZeroMQ の `vram_update` トピックから受け取るピクセルデータを監視します。
    *   イベントループの `paint.Event` 内で、テクスチャイメージを更新（`gl.TexSubImage2D`等）し、`gl.DrawArrays` で四角形を描画したのち `a.Publish()` を発行してリフレッシュさせます。

## 4. 検証シナリオ (Verification Scenarios)

(1) 開発環境にて `cd features/vm` の後、`go run ./cmd/main.go` を実行する。
(2) すぐにネイティブのウィンドウ（Monitor）が起動し、画面が表示される。
(3) バックグラウンドで動作している CPU モジュールがタイマー駆動で発火させ続けている VRAM 操作メッセージ（`draw_pixel`等）を受け取る。
(4) それにしたがって、ウィンドウ上のテクスチャが更新され、目に見える形でピクセルが徐々に描画・アニメーションしていくのを確認する。
(5) ターミナルで `Ctrl+C` を打つ、またはウィンドウを×ボタンで閉じると、VMアプリケーション全体がクリーンにシャットダウンしプロセスが終了する。

## 5. テスト項目 (Testing for the Requirements)

*   **ビルド正常性**:
    *   `./scripts/process/build.sh`
    *   CGO が有効になる（可能性のある） `golang.org/x/mobile` 関係の依存を取り込んだ状態で、全体ビルドがエラーにならないことを担保する。
*   **全体統合の退行チェック**:
    *   `./scripts/process/integration_test.sh --specify "integration"`
    *   今回メインスレッド占有に関する構造変更が入るが、統合テスト側（headless環境下）にデグレードが起きていないことを確認する。
