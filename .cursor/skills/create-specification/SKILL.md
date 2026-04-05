---
name: create-specification
description: Turns user-described requirements into a structured specification markdown under prompts/phases/.../ideas/... Use when starting a feature, capturing background and requirements, or when the user asks for a spec document.
---

# 仕様書作成

ユーザーが述べた内容を元に、構造化された仕様書 (`.../ideas/.../XXX-{Name}.md`) を作成する。

## 1. 準備: ステータスとコンテキストの確認

1. **ステータスの取得**:
    * `scripts/utils/show_current_status.sh` を実行する。
    * JSON出力から `phase`, `branch`, `next_idea_id` を取得する。
    * 以下、`[Phase]`, `[Branch]`, `[NextID]` とする。

## 2. 出力先の決定

1. **ディレクトリの確定**:
    * 基本パス: `prompts/phases/[Phase]/ideas/[Branch]/`
    * 例: `prompts/phases/001-webservices/ideas/main/`
    * このディレクトリが存在しない場合は作成する。
2. **ファイル名の決定**:
    * 形式: `[NextID]-[名前].md`
    * `[名前]` 部分は、仕様の内容を適切に表現する簡潔な名称（例: `Tokenizer`, `RateLimit-GlobalManagement`）。

## 3. 仕様書の内容構成

仕様書には、以下の項目を最低限含める:

1. **背景 (Background)**:
    * なぜこの機能や変更が必要なのか。
    * 現在の課題や問題点。
    * わかる範囲で記載する（不明な場合は省略可）。
2. **要件 (Requirements)**:
    * 実現すべき機能や満たすべき条件。
    * 具体的な振る舞いや制約事項。
    * 必須要件と任意要件を明確に区別する。
3. **実現方針 (Implementation Approach)**:
    * どのような技術やアーキテクチャで実現するか。
    * 主要なコンポーネントやモジュールの概要。
    * 設計上の重要な決定事項。
4. **検証シナリオ (Verification Scenarios)**:
    * **重要 (Preserve Details)**: ユーザーが具体的な手順、条件、またはテストシナリオ（例: 「(1)○○して(2)××する」）を提示した場合は、**要約したり「要件」に丸め込んだりせず、ここにそのままの粒度で転記・整理する。**
    * 「何をもって完了とするか」の具体的なイメージを共有する場所。
    * 形式: 番号付きリストでの時系列記述を推奨。
5. **テスト項目 (Testing for the Requirements)**:
    * 要件が実現されたと確認するための、**自動化された検証手順**を記載する。
    * **重要 (Mandatory Automated Verification)**:
        * 手動確認（「画面を目視で確認」など）だけの計画は許可されない。
        * 必ずプロジェクト標準のスクリプトを使用した検証コマンドを明記する。
        * CLI/Backend/Logic: `scripts/process/build.sh`, `scripts/process/integration_test.sh`
    * どの要件を、どのスクリプト/テストケースで検証するかを対比して記述する。

## 4. 仕様書の作成と保存

1. **ユーザーとの対話**:
    * ユーザーが述べた内容を注意深く聞き、必要に応じて質問して詳細を明確にする。
    * 背景、要件、実現方針、**および具体的な検証シナリオ**の観点で情報を整理する。
    * **警告**: ユーザーが具体的な手順（Scenario）を提示している場合、それを勝手に抽象的な「機能要件」だけに変換して手順を捨ててはいけない。必ず「検証シナリオ」として詳細を残す。
2. **マークダウン形式での作成**:
    * 見出し、リスト、テーブルなどを適切に使用して読みやすく構造化する。
    * コードサンプルやAPIの例があれば、コードブロックで記載する。
    * Mermaid図などを用いて、アーキテクチャや処理フローを視覚化することも推奨される。
3. **ファイルの保存**:
    * 決定したファイル名で、指定されたディレクトリにファイルを保存する。

## 5. 完了確認

1. **内容のレビュー**:
    * 作成した仕様書が、背景・要件・実現方針の観点をカバーしているか確認する。
2. **ファイルパスの提示**:
    * 作成したファイルへのパスをユーザーに提示する（プロジェクトルートからの相対パス）。
    * **フェーズ移行**: 次のステップ（実装計画の作成等）に進むのは、ユーザーが明示的に指示した場合のみ。勝手に `create-implementation-plan` に進まない。
