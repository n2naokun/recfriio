# SKNET MonsterTV U3S (SK-MTVU3S) 動作仕様 & 実装メモ

SKNET MonsterTV U3S (`VID: 3275`, `PID: 9015`) を Linux の Go 版 `recfriio --u3s`（`recmtvu3s` と同じ）で使うときの説明と、ハードウェア構成・制御仕様・検証記録です。  
共通の使い方とオプションは [README](../README.md) にあります。

## Go 版での使い方

C/C++ 版の recfriio の `--u3s`（`Mtvu3s`・`Tc90522`・`Fc0013`・`Qm1d1c0042`）を Go に移したものです。

```bash
bin/recfriio --u3s --stats 21 30 out.ts       # 地デジ
bin/recfriio --stats BS15_0 30 out.ts         # BS（BS・CS は U3S しか受けられないので --u3s は無くてもよい）
bin/recfriio --stats CS2 30 out.ts            # 110 度 CS
```

### U3S での動き

- USB は isochronous（EP `0x84`、altsetting 1、940 バイト × 16 フレーム / URB）で受けます（`internal/usbfs` の `StartIsoStream`）。
- EM28178 が USB 転送でかける AES-128-ECB は、`internal/mtvu3s` の `Descrambler` で外します。  
  `--no-descramble` を付けると外さずに書き出します（照合用。ペイロードは暗号がかかったまま）。
- 受け始めの、復調部が落ち着く前のパケット（TEI 付き。地デジで 1 秒弱）は捨てます（`Settler`）。  
  TEI の無いパケットが 5,000 個続いたら書き出し始めます。`--no-settle` で捨てずに書き出します。
- 衛星は、TMCC に載っている TSID の一覧と突き合わせて TS を選びます。  
  BS は、登録年のビット（`0x0e00`）を無視して当てます（`BS23_1` → `0x4971` など）。  
  CS は表（CS2 = `0x6020` など）から渡します。  
  一覧に無い TS（例: 2026-11-19 の `BS23_2`）を頼むとエラーで止まります。  
  C/C++ 版は黙って別の TS（一覧の最初）を出していました。
- U3S には B-CAS のスロットが無いので、`--b25` はいつも PC/SC のカードリーダーを使います。
- `--stats` はヘッダーだけを数えます。ペイロードは見ないので、暗号がかかったままでも使えます。
- 地デジの C/N は Linux の tc90522 と同じ式です（C/C++ 版の U3S の式とは値が違います）。
- 試験: `internal/mtvu3s/golden_test.go` が、チップの手順で I2C に送るバイト列を、C/C++ 版のクラスで取った記録（`testdata/i2c-golden.txt`）と比べます。

### 確かめた結果

2026-11-19 の実機（`--no-descramble`、暗号を外す前の TS のヘッダーで確かめた結果）:

| チャンネル | 長さ | パケット | 同期の乱れ | TEI | 連続カウンターの飛び | C/N |
|---|---|---|---|---|---|---|
| 地デジ ch21 | 5 秒 | 99,551 | 0 | 0 | 0 | 31.4 dB |
| BS15_0 | 5 秒 | 79,493 | 0 | 0 | 0 | 13.8 dB |
| CS2 | 5 秒 | 173,422 | 0 | 0 | 0 | 10.6 dB |
| 地デジ ch21（頭を捨てない） | 60 秒 | 1,195,083 | 0 | 6,369 | 69 | 31.1 dB |
| 地デジ ch21（頭を捨てる） | 60 秒 | 1,181,834 | 0 | 0 | 0 | — |

頭を捨てないときの TEI は全部、受け始めの 1 秒に固まっていました（ロックの直後で、復調部がまだ落ち着いていない）。  
C/C++ 版の確かめも末尾だけを数えていた（`tail20k_tei`）ので、たぶん同じことが起きています。  
衛星の TS は、PAT の `transport_stream_id`（暗号のかかっていない 8〜9 バイト目）で確かめました（CS2 = `0x6020`、BS15_0 = `0x40f1`、BS01_0 = `0x4010`、BS23_1 = `0x4971`、BS23_3 = `0x4973`）。

AES を外したあと（2026-11-19、3 秒ずつ）:

| チャンネル | PAT の TSID | パケット | 同期の乱れ・TEI・連続カウンターの飛び | PAT の CRC-32 が合ったもの |
|---|---|---|---|---|
| 地デジ 21 | `0x7fe4` | 47,108 | 0 | 23 |
| BS15_0 | `0x40f1` | 47,665 | 0 | 30 |
| CS2 | `0x6020` | 103,978 | 0 | 30 |

`--b25`（外付けのリーダー ACR39U、地デジ 21 で 5 秒）: 87,075 パケット、乱れ 0、スクランブルの残り 0、PAT の CRC-32 が合ったもの 44。

`recfriio` 1 つにまとめたあと（2026-11-19）も、UT100B と同時に動かして、使用中の UT100B を避けて U3S が選ばれること、地デジ 21〜26ch・BS15_0・CS4 で同期の乱れ・TEI・連続カウンターの飛びが 0 であることを確かめました。

---

## 1. ハードウェア構成

| 役割 | チップ | アドレス / 備考 |
|---|---|---|
| USB ブリッジ | **EM28178** | USB 2.0 High-Speed、Isochronous EP `0x84` (`altsetting=1`, `940B x 16/URB`) |
| 地上波復調 (ISDB-T) | **東芝 TC90522** | I2C `0x20` |
| 地上波チューナー | **Fitipower FC0013** | `0x20` リピータ (`0xfe`) 経由 I2C `0xc6` (Chip ID `0xa3`, xtal `28.8 MHz`) |
| 衛星復調 (ISDB-S) | **東芝 TC90522** | I2C `0x22` |
| 衛星チューナー | **シャープ QM1D1C0042** | `0x22` リピータ (`0xfe`) 経由 I2C `0xc0` (xtal `16.0 MHz`) |

```text
USB (EM28178, 3275:9015)
 ├── I2C 0x20: TC90522 (ISDB-T) ──[0xfe]──▶ 0xc6: FC0013 (UHF チューナー)
 └── I2C 0x22: TC90522 (ISDB-S) ──[0xfe]──▶ 0xc0: QM1D1C0042 (BS/CS チューナー)
```

※ MonsterTV U3S には B-CAS スロットは無いため、復号 (`--b25`) を行う場合は外付けの PC/SC カードリーダー（`--pcsc` または環境変数等）を使用します。

---

## 2. 制御プロトコルの要点（`emOEM64.sys` / Windows キャプチャ解析）

### EM28178 USB ベンダリクエスト・GPIO・暗号化
- **ベンダリクエスト**:
  - `0x00`: EM28178 レジスタ R/W
  - `0x02`: I2C R/W (STOP あり)
  - `0x03`: I2C R/W (STOP なし / Repeated Start)
- **初期化と GPIO (`0x80`)**:
  - `0x0c = 0x10`, `0x06 = 0x44`
  - `0x80 = 0x7c` → 50ms 待ち → `0x80 = 0xfd`, `0x73 = 0xfd`（GPIO1=電源 Low ON、GPIO7=リセット Low→High パルス）
- **TS ストリーミングと AES-128-ECB 復号**:
  - 鍵レジスタ `0x60..0x6f` に 16 バイトの鍵（全 `0x00`）を書き込み、`0x5d = 0xef`, `0x5f = 0x89` で TS1 有効化＋同期＋暗号化有効にする。
  - USB (`EP 0x84`) から受信した各 188 バイト TS パケットの `pkt[12..187]`（11 ブロック × 16 バイト）を AES-128-ECB で復号する（`Aes128Ecb.hpp`）。

### TC90522 x2 と 2 チューナーの切替
- 2 基の TC90522 が TS 出力バスを共有するため、使用しない側は TS ピンをトライステートかつスリープにする：
  - **ISDB-T 使用時**: `0x22` 側 `0x1c=0x80, 0x1f=0x22`（トライステート）／`0x20` 側 `0x0e=0x01, 0x0f=0x77, 0x1d=0x00, 0x1f=0x00`、`em28xx 0x50 = 0x01`
  - **ISDB-S 使用時**: `0x20` 側 `0x0e=0x77`（スリープ）, `0x1d=0xa8`（トライステート）／`0x22` 側 `0x1f=0x00, 0x1c=0x05`

### 地デジ (`FC0013` + `TC90522 0x20`) 選局の重要ポイント
- `FC0013` は Zero-IF チューナー（`xtal = 28.8 MHz`, `bw = 6 MHz`）。`emOEM64.sys` (`0x32c78` / `0x330e8` / `0x338de`) 準拠で PLL/VCO 分周とキャリブレーション (`0x0e = 0x80 -> 0x00`) を行い、`0x08 = 0x1f, 0x0c = 0xf8, 0x14 = 0x50` を設定する。
- **選局前後の AGC 制御**: 選局前に `0x20[0x25] = 0x00`, `0x20[0x23] |= 0x01` を設定し、選局後に `0x20[0x23] &= ~0x01`, `0x20[0x25] = 0x80` へ戻してから `0x20[0x01] = 0x40` でリセットする。その後ロック待ちループ内で `Fc0013::adjustLna()` (`0x33c00` 準拠) を呼ぶ。

---

## 3. ソース構成

2026-11-19 に Go 版（`recfriio --u3s`、`recmtvu3s` と同じ）へ置き換えました。下の表は C++ 版（タグ `cpp-final` の `recfriior5/`）のもので、Go 版の対応は次のとおりです。

| C++ 版 | Go 版 |
|---|---|
| `Mtvu3s.cpp`（EM28178・選局・TS の区切り） | `internal/em28178/`・`internal/mtvu3s/` |
| `Tc90522.cpp` | `internal/tc90522/` |
| `Fc0013.cpp` | `internal/fc0013/` |
| `Qm1d1c0042.cpp` | `internal/qm1d1c0042/` |
| `I2cBus.hpp` | `internal/i2c/` |
| `IoThread.cpp` の isochronous の URB | `internal/usbfs/stream.go`（`StartIsoStream`） |
| `recfriio --u3s` | `recfriio --u3s`（`recmtvu3s` と同じ） |

C++ 版のファイル:

| ファイル | 役割 |
|---|---|
| `Mtvu3s.hpp` / `Mtvu3s.cpp` | `AbstractFriio` 実装、EM28178 制御、TS 同期・AES 復号、バンド/チャンネル切替 |
| `I2cBus.hpp` | I2C バス抽象 (`I2cBus`)、`RegVal`、`waitUntil()` |
| `Tc90522.hpp` / `Tc90522.cpp` | TC90522 復調器制御および `0xfe` リピータ I2C バス |
| `Fc0013.hpp` / `Fc0013.cpp` | FC0013 (ISDB-T) 初期化・PLL/VCO 選局・LNA 調整 |
| `Qm1d1c0042.hpp` / `Qm1d1c0042.cpp` | QM1D1C0042 (ISDB-S) 初期化・選局 |
| `Aes128Ecb.hpp` | スタンドアロン AES-128-ECB 復号器 |

---

## 4. C++ 版での実機検証結果 (2026-11-19)

実機 (`Bus 003`, `3275:9015`) にてリファクタリング後のコードで以下を確認済み：

| テスト項目 | 結果 |
|---|---|
| `make -C recfriior5 test` (`test_stream_cipher`) | 3 ケースすべて `ok` |
| 地デジ `ch21` (`recfriio --u3s 21 2`) | `57,763 pkts`, `sync_err=0`, `tail20k_tei=0`, PAT CRC-32 OK `25`, `Signal level: 22.16 -> 34.03 dB` |
| BS `BS15_0` (`recfriio --u3s BS15_0 2`) | `42,602 pkts`, `sync_err=0`, `tail20k_tei=0`, PAT CRC-32 OK `27`, `Signal level: 13.61 -> 13.54 dB` |
| CS `CS2` (`recfriio --u3s CS2 2`) | `65,546 pkts`, `sync_err=0`, `tail20k_tei=0`, PAT CRC-32 OK `19`, `Signal level: 10.19 -> 10.39 dB` |
| 2 台同時録画 (`--u3s BS15_0` + `--hdp 21`, 各 3 秒) | MonsterTV U3S (`55,830 pkts`) / QRS-UT100B (`40,436 pkts`) ともに `sync_err=0`, `tail20k_tei=0`, PAT CRC-32 OK 各 `35` |
