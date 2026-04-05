---
name: review-point
description: Pauses workflow progression until the user explicitly approves the next phase. Use when finishing specification or implementation-plan work, or when the user wants a human review gate before create-implementation-plan, execute-implementation-plan, or build-pipeline.
---

# Review Point（ワークフロー一時停止）

各フェーズ（仕様策定、実装計画、実装実行）の間に挟み、意図しない自動進行を防ぎ、ユーザーによる十分なレビューと確認時間を確保する。

## 実行手順

1. **現状の確認**
   - 直前の作業で生成・更新された成果物（仕様書、実装計画書、コードなど）を確認する。
   - ユーザーからの質問や修正依頼がある場合は、それに対応する。

2. **待機状態の維持**
   - ユーザーから「次のフェーズへ進む」「実装計画を作成して」「実装を実行して」などの**明示的な指示**があるまでは、**次のスキルに基づく一連の作業を自動的に開始しない**。
   - 修正や議論が必要な場合は、この Review Point の状態にとどまり、対話を行う。

3. **次のステップの案内**
   - ユーザーに現状の成果物がOKであれば、次に使うスキルを提示する。
   - 例:
     - 仕様書が完成した場合: 「宜しければ実装計画作成スキル（create-implementation-plan）に従って計画を作成できます。」
     - 実装計画が完成した場合: 「宜しければ実装実行スキル（execute-implementation-plan）に従って実装を開始できます。」

## 禁止事項

- ユーザーの明示的な許可なしに、以下を勝手に開始すること。
  - 仕様書作成（create-specification）の**次フェーズ**としての実装計画作成
  - 実装計画作成（create-implementation-plan）の**次フェーズ**としての実装実行
  - 実装実行（execute-implementation-plan）の**次フェーズ**としての本番コミット等（プロジェクト方針に従う）

（単体のスキル実行をユーザーが依頼した場合はその限りではない。）
