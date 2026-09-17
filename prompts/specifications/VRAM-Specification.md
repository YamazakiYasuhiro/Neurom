# VRAMの仮想ハードウェア仕様: 機能説明とコマンド一覧

## 1. この文書の位置付け

本書は VRAM モジュールがバス上で受け付けるコマンドと発行するイベントの**契約書**である。

従来、バイトレイアウトの定義は送信側（`internal/modules/cpu/cpu.go` の `publish*`）と
受信側（`internal/modules/vram/vram.go` の `handle*`）に二重に手書きされており、
実装だけが唯一の真実であった。現在はこの契約は `features/neurom/arcproto` パッケージに
コードとして一元化されている。

- **正典**: `features/neurom/arcproto`（Encode / Decode が対称に定義されている）
- **本書の役割**: `arcproto` が表現している契約を、他言語実装者と設計判断のために散文で記述する

本書と `arcproto` が食い違った場合は `arcproto` を正とし、本書を修正する。

## 2. 共通規定

| 項目 | 規定 |
| :--- | :--- |
| バイト順 | 全ての多バイト整数はビッグエンディアン |
| 座標・サイズ | `u16` |
| ビューポートオフセット | `i16`（2 の補数）。負値を取る唯一のフィールド |
| ページ番号 | `u8` |
| パレットインデックス | `u8`（256 段） |
| パレット色 | RGBA 各 8bit |
| 回転 | `u8`。0-255 で一周（`arcproto.Rotation`）。`float64(rotation)/256.0*2π` として解釈される |
| 拡大率 | `u16`。8.8 固定小数点で `0x0100` が等倍（`arcproto.Scale`）。**0 以下は等倍に丸められる** |

### 2-1. デコードの寛容性

受信側は以下の規則で解釈する。

- 固定長部分に足りない payload は**破棄する**（エラーを返さず、イベントも発行しない）
- 末尾の余剰バイトは**無視する**。これによりコマンドを後方互換に拡張できる
- 例外として `clear_vram` は両フィールドが省略可能で、欠けたフィールドは 0 とみなす

### 2-2. ブレンドモード

| 値 | 名称 | 演算 |
| :--- | :--- | :--- |
| `0x00` | Replace | dst = src（合成なし） |
| `0x01` | Alpha | dst = src×α + dst×(1-α) |
| `0x02` | Additive | dst = min(src×α + dst, 255) |
| `0x03` | Multiply | dst = src × dst / 255 |
| `0x04` | Screen | dst = 255 - (255-src)×(255-dst)/255 |

`Replace` 以外のモードで描画されたピクセルは、パレットインデックスではなく
直接色として保持される（インデックス値 `0xFF` = `DirectColorMarker` が記録される）。
未定義の値が来た場合は `Replace` と同じ扱いになる。

## 3. トピック

| トピック | 用途 |
| :--- | :--- |
| `vram` | VRAM へのコマンド |
| `vram_update` | VRAM からのイベント |
| `monitor` / `monitor_update` | モニタへのコマンドとイベント |
| `system` | シャットダウン等のシステム制御 |
| `io` | 入力デバイス |

> [!WARNING]
> **既知の欠陥: 購読が前方一致である。**
> `ChannelBus.Publish` は `strings.HasPrefix(topic, subTopic)` で配信先を決めるため、
> `vram` を購読した者は `vram_update` のイベントも受信してしまう。
> 結果として VRAM モジュールは自分が発行したイベントを自分で受け取っている。
> 仕様 025 で階層的な命名と完全一致による是正を予定している。

## 4. コマンド一覧（全 17 件）

サイズは固定長部分の長さ。「不正時」は固定長に足りない場合を除いた異常系の挙動。

### 4-1. 描画

| Target | レイアウト | サイズ | 発行イベント | 不正時 |
| :--- | :--- | :--- | :--- | :--- |
| `mode` | 参照されない | 3 | `mode_changed` | — |
| `draw_pixel` | `[page:u8][x:u16][y:u16][p:u8]` | 6 | `vram_updated` | ページ不正で `page_error 0x01`。範囲外座標は無言で無視（イベントも出ない） |
| `clear_vram` | `[page:u8][palette_idx:u8]` | 0-2 | `vram_cleared` | ページ不正で `page_error 0x01` |
| `blit_rect` | `[page:u8][x:u16][y:u16][w:u16][h:u16][blend:u8][pixels...]` | 10+ | `rect_updated` | ページ不正で `page_error 0x01`。`pixels` が `w×h` に足りなければ破棄。クリップ後が空なら描画せずイベントも出ない |
| `blit_rect_transform` | `[page:u8][x:u16][y:u16][src_w:u16][src_h:u16][pivot_x:u16][pivot_y:u16][rotation:u8][scale_x:u16][scale_y:u16][blend:u8][pixels...]` | 19+ | `rect_updated` | 同上。変換結果が空なら破棄 |
| `copy_rect` | `[src_page:u8][dst_page:u8][src_x:u16][src_y:u16][dst_x:u16][dst_y:u16][w:u16][h:u16]` | 14 | `rect_copied` | いずれかのページ不正で `page_error 0x01` |

`mode` はページ 0 を既定サイズで再初期化する。
`blit_rect_transform` の `x` / `y` は矩形の左上ではなく**ピボットの位置**を指し、
描画される領域は `src_w`×`src_h` より大きくなり得る。
`copy_rect` は転送元と転送先が重なっても安全である（一旦別バッファに退避してから書き戻す）。

### 4-2. パレット

| Target | レイアウト | サイズ | 発行イベント | 備考 |
| :--- | :--- | :--- | :--- | :--- |
| `set_palette` | `[index:u8][R][G][B]` または `[index:u8][R][G][B][A]` | 4 または 5 | `palette_updated` | **アルファは省略可**。省略時は 255 |
| `set_palette_block` | `[start:u8][count:u8]` + `[R][G][B][A]` × count | 2+ | `palette_block_updated` | `count` 分の色データが無ければ破棄。インデックス 255 を超える分は捨てられる |
| `read_palette_block` | `[start:u8][count:u8]` | 2 | `palette_data` | インデックス 255 を超える分は 0 で埋まる |

### 4-3. ページ管理

| Target | レイアウト | サイズ | 発行イベント | 不正時 |
| :--- | :--- | :--- | :--- | :--- |
| `set_page_count` | `[count:u8]` | 1 | `page_count_changed` | — |
| `set_display_page` | `[page:u8]` | 1 | `display_page_changed` | ページ不正で `page_error` **`0x03`** |
| `swap_pages` | `[page1:u8][page2:u8]` | 2 | `pages_swapped` | ページ不正で `page_error 0x01` |
| `copy_page` | `[src:u8][dst:u8]` | 2 | `page_copied` | ページ不正で `page_error 0x01`。`src == dst` なら何もせずイベントも出ない |
| `set_page_size` | `[page:u8][w:u16][h:u16]` | 5 | `page_size_changed` | ページ不正で `page_error 0x01` |

`set_page_count` の `count` は **0 が 256 を意味する**。
現在の枚数より増やす場合は既定サイズ（256×212）で確保され、減らす場合は超過分が破棄される。
表示ページが新しい枚数の外に出る場合は 0 に戻される。

`swap_pages` はピクセルデータを複製せずページを入れ替えるため、
ダブルバッファリングの表示切り替えに使う。
`copy_page` は寸法も含めて複製する。
`set_page_size` は再確保を伴うため**内容が消去される**。

### 4-4. 読み出しと診断

| Target | レイアウト | サイズ | 発行イベント | 不正時 |
| :--- | :--- | :--- | :--- | :--- |
| `read_rect` | `[page:u8][x:u16][y:u16][w:u16][h:u16]` | 9 | `rect_data` | ページ不正で `page_error 0x01`。ページ外のピクセルはインデックス 0 として読み出される |
| `set_viewport` | `[off_x:i16][off_y:i16]` | 4 | `viewport_changed` | — |
| `get_stats` | payload なし | 0 | `stats_data` | — |

`set_viewport` は表示ページ上の可視窓をずらす。ページ内容は動かないため、
これがハードウェアスクロールの実現手段である。負のオフセットで左・上へ動かせる。

## 5. イベント一覧（全 17 件）

| Target | payload | 発行契機 |
| :--- | :--- | :--- |
| `mode_changed` | なし | `mode` 完了 |
| `vram_updated` | `draw_pixel` の payload をそのままエコー | `draw_pixel` が範囲内に描画した |
| `vram_cleared` | `[page:u8][palette_idx:u8]` | `clear_vram` 完了 |
| `rect_updated` | `[x:u16][y:u16][w:u16][h:u16]` | `blit_rect` / `blit_rect_transform` 完了 |
| `rect_copied` | `copy_rect` の payload をそのままエコー | `copy_rect` 完了 |
| `rect_data` | `[x:u16][y:u16][w:u16][h:u16][pixels...]` | `read_rect` への応答 |
| `palette_updated` | `set_palette` の payload をそのままエコー | `set_palette` 完了 |
| `palette_block_updated` | `[start:u8][count:u8]`（**先頭 2 バイトのみ**） | `set_palette_block` 完了 |
| `palette_data` | `[start:u8][count:u8]` + `[R][G][B][A]` × count | `read_palette_block` への応答 |
| `page_count_changed` | `[count:u8]` | `set_page_count` 完了 |
| `display_page_changed` | `[page:u8]` | `set_display_page` 完了 |
| `pages_swapped` | `[page1:u8][page2:u8]` | `swap_pages` 完了 |
| `page_copied` | `[src:u8][dst:u8]` | `copy_page` 完了 |
| `page_size_changed` | `[page:u8][w:u16][h:u16]` | `set_page_size` 完了 |
| `viewport_changed` | `[off_x:i16][off_y:i16]` | `set_viewport` 完了 |
| `page_error` | `[code:u8]` | ページ指定が不正なコマンドを拒否した |
| `stats_data` | JSON | `get_stats` への応答 |

`rect_updated` が示す領域は、`blit_rect` ではクリップ後の矩形、
`blit_rect_transform` では**変換後の外接矩形**であり、元の矩形より大きくなり得る。
モニタはこの領域だけを再描画する。

`palette_block_updated` が先頭 2 バイトのみを返すのは、
どの範囲が変わったかを伝えるために色データの複製を避けているためである。

## 6. エラーコード

| コード | 意味 |
| :--- | :--- |
| `0x01` | 無効なページ番号 |
| `0x03` | 無効な表示ページ指定（`set_display_page` 専用） |

> [!NOTE]
> **`0x02` は欠番である。** 現行実装がこの値を発行する経路は存在しない。
> 歴史的な理由は不明であり、新規のエラーコードを割り当てる際に再利用しないこと。

`page_error` には**どのコマンドが失敗したかを示す情報が含まれない**。
コマンド自体は破棄される。

## 7. 座標系とクリッピング規則

原点は左上、x が右方向、y が下方向である。

描画コマンドは矩形をページ境界に合わせて切り詰める（`clipRect`）。

1. 負の座標は 0 に切り上げ、**その分を転送元のオフセットとして吸収する**
   （矩形の左端が欠けるのではなく、転送元の対応する位置から読み始める）
2. 右端・下端がページの幅・高さを超える分は切り詰める
3. 切り詰めた結果、幅または高さが 0 以下になった場合は**何も描画せず、イベントも発行しない**

`read_rect` はこの規則の対象外で、ページ外の座標はインデックス 0 として読み出される。

## 8. 既知の制約

いずれも後続の仕様で解決を予定している。

- **`rect_data` に読み出し元ページが含まれない。** 要求元が自分で覚えておく必要がある
- **読み出しコマンドに相関 ID が無い。** `read_rect` / `read_palette_block` を並行して
  発行すると、どの応答がどの要求に対応するか判別できない（025 で解決予定）
- **`mode` コマンドの payload は参照されない。** `cpu.go` が送る 3 バイト `00 01 00` は
  互換のために維持しているだけで、意味は不明である
- **`set_page_size` に上限検証が無い。** 過大な値で大量のメモリを確保できる（028 で解決予定）
- **バスは購読チャネルが満杯のときメッセージを黙って破棄する。**
  高負荷時にコマンドが失われても送信側は気付けない（025 で可視化予定）
- **1 コマンド = 1 メッセージであり、フレーム単位の原子性が無い。**
  描画途中の状態が表示される可能性がある（025 でバッチ化を予定）

## 9. 既存仕様書との齟齬

`prompts/phases/000-foundation/branches/main/ideas/012-VRAMPage.md`（83-84 行、164-172 行）は
`blit_rect` / `blit_rect_transform` のレイアウトを
`[src_page:u8][dst_page:u8]` の 2 ページ指定として記述しており、
`src_page` を「ブレンド時に背景ピクセルを読み取るページ」と定義している。

**実装は転送先の `[page:u8]` 1 個のみ**であり、ブレンドの背景は常に転送先ページ自身から読む。
すなわちフィールド数だけでなく、ページを分離したブレンドという機能自体が実装されていない。
同文書のシナリオ 8（`src_page` ≠ `dst_page` でのブレンド）も現行実装では成立しない。

本書と `arcproto` の記述を正とする。ページ分離ブレンドを将来必要とする場合は、
新規コマンドとして設計すること（既存の `blit_rect` のレイアウト変更は
拡張ソフトウェアとの互換性を破壊する）。
