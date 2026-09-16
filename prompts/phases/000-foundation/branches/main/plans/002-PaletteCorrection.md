# 002-PaletteCorrection

> **Source Specification**: prompts/phases/000-foundation/ideas/main/002-PaletteCorrection.md

## Goal Description
`monitor.go` 内における 8-bit から RGB へのマッピング処理で発生しているバイト整数の乗算によるオーバーフローを修正し、モニターウィンドウの画素が意図せず極端に暗くなってしまうバグを修正する。

## User Review Required
None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R-001: パレット計算式のオーバーフロー修正 | `Proposed Changes > features/vm/internal/modules/monitor` |
| R-002: 256色（8-bit）カラー空間への完全な展開 | `Proposed Changes > features/vm/internal/modules/monitor` |

## Proposed Changes

### Monitor Module

#### [MODIFY] features/vm/internal/modules/monitor/monitor.go
*   **Description**: VRAMデータ受信時の RGB への変換式において `int` キャストを使用した拡張演算へ修正。
*   **Technical Design**:
    *   修正前:
        ```go
        r := byte(((p >> 5) & 7) * 255 / 7)
        g := byte(((p >> 2) & 7) * 255 / 7)
        b := byte((p & 3) * 255 / 3)
        ```
    *   修正後:
        ```go
        r := byte((int((p >> 5) & 7) * 255) / 7)
        g := byte((int((p >> 2) & 7) * 255) / 7)
        b := byte((int(p & 3) * 255) / 3)
        ```
*   **Logic**:
    *   Go言語ではビットシフトした際の右辺型推論により、 `((p >> 5) & 7)` がそのまま元変数の `uint8` として評価され、`* 255` で255を超えた値が切り捨てられます。
    *   これを避けるため明示的に `int`として計算させた後に `byte` へ落とし込みます。これによりピクセル本来の白色(255)を出力可能にします。

## Step-by-Step Implementation Guide

1.  **[x] Step 1: Fix Palette Math Overflow**:
    *   Edit `features/vm/internal/modules/monitor/monitor.go` to update mapping logic around R, G, B computing lines inside `receiveLoop`.

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
    ./scripts/process/integration_test.sh --specify "integration"
    ```
    *   **Log Verification**: 再コンパイルとテストが正常に通ることをビルドスクリプトと統合スクリプトで担保。

## Documentation
*更新対象のドキュメントはありません。*
