#include <unistd.h>
#include <string.h>
#include <math.h>
#include <sstream>
#include <iostream>

#include "Mtvu3s.hpp"
#include "usbops.hpp"
#include "setting.hpp"

namespace {
const uint8_t DEMOD_TER_ADDR = 0x20;
const uint8_t DEMOD_SAT_ADDR = 0x22;
const uint8_t TUNER_SAT_ADDR = 0xc0;
const uint8_t TUNER_TER_ADDR = 0xc6;
const size_t  TS_PACKET_SIZE = 188;
const size_t  TS_AES_OFFSET  = 12;
const size_t  TS_AES_BLOCKS  = 11; // 11 * 16 = 176 bytes
}

Mtvu3s::Mtvu3s()
	: currentBand(BAND_UHF), synced(false)
{
	targetType         = TUNER_MTVU3S;
	endpoint           = TARGET_ENDPOINT_MTVU3S;
	asyncBufSize       = MTVU3S_ASYNCBUFFSIZE;
	requestReserveNum  = MTVU3S_REQUEST_RESERVE_NUM;
	requestPollingWait = MTVU3S_REQUEST_POLLING_WAIT;
	isoPacketSize      = MTVU3S_ISO_PACKET_SIZE;
	isoPacketsPerUrb   = MTVU3S_ISO_PACKETS_PER_URB;

	supportBands.push_back(BAND_UHF);
	supportBands.push_back(BAND_BS);
	supportBands.push_back(BAND_CS);

	memset(qmRegs, 0, sizeof(qmRegs));
	packBuf.reserve(65536);
	outBuf.reserve(65536);
}

Mtvu3s::~Mtvu3s()
{
	if (initialized) {
		close();
	}
}

uint8_t Mtvu3s::emRegRead(int fd, uint8_t reg)
{
	uint8_t val = 0;
	usbdevfs_ctrltransfer ctrl = {
		USB_DIR_IN | USB_TYPE_VENDOR | USB_RECIP_DEVICE,
		0x00, 0, reg, 1, REQUEST_TIMEOUT, &val
	};
	usb_ctrl(fd, &ctrl);
	return val;
}

void Mtvu3s::emRegWrite(int fd, uint8_t reg, uint8_t val)
{
	usbdevfs_ctrltransfer ctrl = {
		USB_DIR_OUT | USB_TYPE_VENDOR | USB_RECIP_DEVICE,
		0x00, 0, reg, 1, REQUEST_TIMEOUT, &val
	};
	usb_ctrl(fd, &ctrl);
}

void Mtvu3s::i2cWrite(int fd, uint8_t addr, const uint8_t *data, uint16_t len, bool stop)
{
	uint8_t buf[256];
	memcpy(buf, data, len);
	usbdevfs_ctrltransfer ctrl = {
		USB_DIR_OUT | USB_TYPE_VENDOR | USB_RECIP_DEVICE,
		static_cast<uint8_t>(stop ? 0x02 : 0x03),
		0, addr, len, REQUEST_TIMEOUT, buf
	};
	usb_ctrl(fd, &ctrl);
}

void Mtvu3s::i2cRead(int fd, uint8_t addr, uint8_t *data, uint16_t len, bool stop)
{
	usbdevfs_ctrltransfer ctrl = {
		USB_DIR_IN | USB_TYPE_VENDOR | USB_RECIP_DEVICE,
		static_cast<uint8_t>(stop ? 0x02 : 0x03),
		0, addr, len, REQUEST_TIMEOUT, data
	};
	usb_ctrl(fd, &ctrl);
}

void Mtvu3s::demodWrite(int fd, uint8_t addr, uint8_t reg, uint8_t val)
{
	uint8_t buf[2] = { reg, val };
	i2cWrite(fd, addr, buf, 2, true);
}

void Mtvu3s::demodRead(int fd, uint8_t addr, uint8_t reg, uint8_t *buf, uint16_t len)
{
	i2cWrite(fd, addr, &reg, 1, false);
	i2cRead(fd, addr, buf, len, true);
}

void Mtvu3s::tunerWrite(int fd, uint8_t demodAddr, uint8_t tunerAddr, const uint8_t *data, uint16_t len)
{
	uint8_t buf[256];
	buf[0] = 0xfe;
	buf[1] = tunerAddr;
	memcpy(&buf[2], data, len);
	i2cWrite(fd, demodAddr, buf, static_cast<uint16_t>(len + 2), true);
}

uint8_t Mtvu3s::tunerReadReg(int fd, uint8_t demodAddr, uint8_t tunerAddr, uint8_t reg)
{
	uint8_t wr[3] = { 0xfe, tunerAddr, reg };
	i2cWrite(fd, demodAddr, wr, 3, true);
	uint8_t rd[2] = { 0xfe, static_cast<uint8_t>(tunerAddr | 0x01) };
	i2cWrite(fd, demodAddr, rd, 2, false);
	uint8_t val = 0;
	i2cRead(fd, demodAddr, &val, 1, true);
	return val;
}

bool Mtvu3s::is_friio(const std::string &devfile)
{
	usb_device_descriptor usb_desc;
	try {
		usb_getdesc(devfile.c_str(), &usb_desc);
	} catch (usb_error &e) {
		if (log) *log << "usb_getdesc: " << e.what() << std::endl;
		return false;
	}
	return (TARGET_ID_VENDOR_HDUS == usb_desc.idVendor &&
	        TARGET_ID_PRODUCT_MTVU3S == usb_desc.idProduct);
}

TunerType Mtvu3s::UsbProcFixInitA(int /*fd*/)
{
	return TUNER_MTVU3S;
}

void Mtvu3s::initDemods(int fd)
{
	// ISDB-T (0x20): クロック設定(0x0e=0x01) + 公式テーブル(0xfbd50) + TSピンは一旦トライステート
	static const uint8_t terInit[][2] = {
		{0x0e, 0x01}, {0x0f, 0x77}, {0x01, 0x40}, {0x20, 0x00},
		{0x21, 0xff}, {0x22, 0x8f}, {0x23, 0xfc}, {0x24, 0x4c},
		{0x25, 0x80}, {0xf4, 0x80}, {0x1d, 0xa8}
	};
	for (const auto &kv : terInit) {
		demodWrite(fd, DEMOD_TER_ADDR, kv[0], kv[1]);
	}

	// ISDB-S (0x22): 公式テーブル(0xfc350) + 0x07/0x08クロック設定 + TSピン一旦トライステート
	static const uint8_t satInit[][2] = {
		{0x07, 0x01}, {0x08, 0x10}, {0x15, 0x00}, {0x1d, 0x00},
		{0x0a, 0xff}, {0x10, 0xb1}, {0x11, 0x40}, {0x85, 0x7a},
		{0x87, 0x04}, {0x1c, 0x80}, {0x1f, 0x22}, {0x03, 0x01}
	};
	for (const auto &kv : satInit) {
		demodWrite(fd, DEMOD_SAT_ADDR, kv[0], kv[1]);
	}
}

void Mtvu3s::initQm1d1c0042(int fd)
{
	static const uint8_t regInit[32] = {
		0x68, 0x1c, 0xb0, 0x10, 0xbc, 0xc1, 0x11, 0x35,
		0x09, 0x06, 0x00, 0x00, 0xc3, 0x47, 0xb5, 0x30,
		0x00, 0xff, 0xf3, 0x20, 0x3f, 0x25, 0x5c, 0xd6,
		0x55, 0xcf, 0x95, 0xf6, 0x36, 0xf2, 0x09, 0x00
	};
	memcpy(qmRegs, regInit, sizeof(qmRegs));
	for (uint8_t r = 1; r < 32; r++) {
		uint8_t msg[2] = { r, qmRegs[r] };
		tunerWrite(fd, DEMOD_SAT_ADDR, TUNER_SAT_ADDR, msg, 2);
	}
}

void Mtvu3s::initFc0013(int fd)
{
	static const uint8_t initSeq[][2] = {
		{0x01, 0x09}, {0x02, 0x16}, {0x03, 0x00}, {0x04, 0x00},
		{0x05, 0x17}, {0x06, 0x02}, {0x07, 0x2a}, {0x08, 0xff},
		{0x09, 0x6f}, {0x0a, 0xb8}, {0x0b, 0x82}, {0x0c, 0xfe},
		{0x0d, 0x01}, {0x0e, 0x00}, {0x0f, 0x00}, {0x10, 0x00},
		{0x11, 0x00}, {0x12, 0x00}, {0x13, 0x00}, {0x14, 0x50},
		{0x15, 0x01}
	};
	for (const auto &kv : initSeq) {
		tunerWrite(fd, DEMOD_TER_ADDR, TUNER_TER_ADDR, kv, 2);
	}
	usleep(50 * 1000);
}

void Mtvu3s::adjustFc0013Lna(int fd)
{
	// emOEM64.sys 0x33c00: RSSI (0x12/0x13) に応じて LNA (0x14) を調整
	uint8_t r14 = tunerReadReg(fd, DEMOD_TER_ADDR, TUNER_TER_ADDR, 0x14);
	const uint8_t zero12[2] = { 0x12, 0x00 };
	const uint8_t zero13[2] = { 0x13, 0x00 };
	tunerWrite(fd, DEMOD_TER_ADDR, TUNER_TER_ADDR, zero12, 2);
	tunerWrite(fd, DEMOD_TER_ADDR, TUNER_TER_ADDR, zero13, 2);
	uint8_t r12 = tunerReadReg(fd, DEMOD_TER_ADDR, TUNER_TER_ADDR, 0x12);
	uint8_t r13 = tunerReadReg(fd, DEMOD_TER_ADDR, TUNER_TER_ADDR, 0x13);

	static const int lnaTable[8] = { 10, 8, 4, 6, 0, 0, 0, 2 };
	static const int pgaTable[4] = { 0, 2, 6, 4 };
	int lna = lnaTable[(r13 >> 5) & 0x07];
	int pga = pgaTable[(r12 >> 2) & 0x03];
	int ifg = pgaTable[r12 & 0x03];
	int rf  = (r13 & 0x1f) * 2;
	int tot = lna + pga + ifg + rf;

	uint8_t mode = r14 & 0x1f;
	uint8_t next = r14;
	if (mode == 0x00 && tot > 0x1e) {
		next = static_cast<uint8_t>((r14 & 0xe0) | 0x08);
	} else if (mode == 0x08) {
		if (tot < 0x06) next = static_cast<uint8_t>(r14 & 0xe0);
		else if (tot > 0x32) next = static_cast<uint8_t>((r14 & 0xf0) | 0x10);
	} else if (mode == 0x10 && tot < 0x1c) {
		next = static_cast<uint8_t>((r14 & 0xe0) | 0x08);
	} else if (mode != 0x00 && mode != 0x08 && mode != 0x10) {
		next = static_cast<uint8_t>((r14 & 0xf0) | 0x10);
	}
	if (next != r14) {
		uint8_t msg[2] = { 0x14, next };
		tunerWrite(fd, DEMOD_TER_ADDR, TUNER_TER_ADDR, msg, 2);
	}
}

void Mtvu3s::UsbProcBegin(int fd)
{
	if (log) *log << "Mtvu3s UsbProcBegin..." << std::endl;

	// EM28178 ウェイクアップ＆TC90522 リセット解除 (GPIO1=ON(Low), GPIO7=Low->High)
	emRegWrite(fd, 0x0c, 0x10);
	emRegWrite(fd, 0x06, 0x44);
	emRegWrite(fd, 0x80, 0x7c);
	usleep(50 * 1000);
	emRegWrite(fd, 0x80, 0xfd);
	emRegWrite(fd, 0x73, 0xfd);
	usleep(50 * 1000);

	initDemods(fd);
	initQm1d1c0042(fd);
	initFc0013(fd);

	// EM28178 AES-128-ECB キー (全ゼロ) と TS キャプチャ設定
	for (uint8_t i = 0; i < 16; i++) {
		emRegWrite(fd, static_cast<uint8_t>(0x60 + i), 0x00);
	}
	emRegWrite(fd, 0x5d, 0xef);
	uint8_t zeroKey[16] = {};
	aes.setKey(zeroKey);
}

void Mtvu3s::UsbProcEnd(int fd)
{
	if (log) *log << "Mtvu3s UsbProcEnd..." << std::endl;
	emRegWrite(fd, 0x5f, 0x08);
	usb_setinterface(fd, TARGET_INTERFACE, TARGET_ALTSETTING);
}

void Mtvu3s::UsbProcStreamInit(int /*fd*/) {}
void Mtvu3s::UsbProcLED(int /*fd*/, BonLedColor /*color*/, bool /*lnb_powered*/) {}
void Mtvu3s::UsbProcFixInitB(int /*fd*/) {}

void Mtvu3s::tuneSat(uint32_t freqKhz, uint16_t reqTsid)
{
	// ISDB-T 側 TS ピンをトライステート＆スリープにし、ISDB-S 側を有効化
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x0e, 0x77);
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x1d, 0xa8);
	demodWrite(tunerFd, DEMOD_SAT_ADDR, 0x1f, 0x00);
	demodWrite(tunerFd, DEMOD_SAT_ADDR, 0x1c, 0x05);

	// QM1D1C0042 選局計算 (xtal = 16000 kHz)
	static const uint32_t convTable[9][3] = {
		{ 2151000, 1, 7 }, { 1950000, 1, 6 }, { 1800000, 1, 5 },
		{ 1600000, 1, 4 }, { 1450000, 1, 3 }, { 1250000, 1, 2 },
		{ 1200000, 0, 7 }, {  975000, 0, 6 }, {  950000, 0, 0 }
	};
	const uint32_t xtal = 16000;
	qmRegs[0x08] = (qmRegs[0x08] & 0xf0) | 0x02;
	qmRegs[0x13] = (qmRegs[0x13] & 0x9f) | 0x20;

	uint8_t val02 = qmRegs[0x02] & 0x0f;
	for (int i = 0; i < 8; i++) {
		if (freqKhz < convTable[i][0] && freqKhz >= convTable[i + 1][0]) {
			val02 |= static_cast<uint8_t>((convTable[i][1] << 7) | (convTable[i][2] << 4));
			break;
		}
	}
	qmRegs[0x02] = val02;

	uint32_t a = (freqKhz + xtal / 2) / xtal;
	qmRegs[0x06] = (qmRegs[0x06] & 0x40) | static_cast<uint8_t>((a - 12) / 4);
	qmRegs[0x07] = (qmRegs[0x07] & 0xf0) | static_cast<uint8_t>((a - 4 * ((a - 12) / 4 + 1) - 5) & 0x0f);

	int64_t b = static_cast<int64_t>((static_cast<uint64_t>(freqKhz) << 20) / xtal) - (static_cast<int64_t>(a) << 20);
	uint32_t sd = (b >= 0) ? static_cast<uint32_t>(b) : static_cast<uint32_t>((1 << 22) + b);
	qmRegs[0x09] = (qmRegs[0x09] & 0xc0) | static_cast<uint8_t>((sd >> 16) & 0x3f);
	qmRegs[0x0a] = static_cast<uint8_t>((sd >> 8) & 0xff);
	qmRegs[0x0b] = static_cast<uint8_t>(sd & 0xff);

	const uint8_t regsToWrite[] = { 0x02, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x13 };
	for (uint8_t r : regsToWrite) {
		uint8_t msg[2] = { r, qmRegs[r] };
		tunerWrite(tunerFd, DEMOD_SAT_ADDR, TUNER_SAT_ADDR, msg, 2);
	}

	uint8_t c0[2] = { 0x0c, static_cast<uint8_t>(qmRegs[0x0c] & 0x3f) };
	tunerWrite(tunerFd, DEMOD_SAT_ADDR, TUNER_SAT_ADDR, c0, 2);
	usleep(3000);
	uint8_t c1[2] = { 0x0c, static_cast<uint8_t>(qmRegs[0x0c] | 0xc0) };
	tunerWrite(tunerFd, DEMOD_SAT_ADDR, TUNER_SAT_ADDR, c1, 2);
	usleep(50 * 1000);

	uint8_t lpf[2] = { 0x08, 0x09 };
	tunerWrite(tunerFd, DEMOD_SAT_ADDR, TUNER_SAT_ADDR, lpf, 2);
	uint8_t csel[2] = { 0x13, qmRegs[0x13] };
	tunerWrite(tunerFd, DEMOD_SAT_ADDR, TUNER_SAT_ADDR, csel, 2);

	// 初期リセット＆TMCC ロック待ち
	demodWrite(tunerFd, DEMOD_SAT_ADDR, 0x03, 0x01);
	for (int i = 0; i < 40; i++) {
		usleep(25 * 1000);
		uint8_t c3 = 0xff;
		demodRead(tunerFd, DEMOD_SAT_ADDR, 0xc3, &c3, 1);
		if (!(c3 & 0x10)) break;
	}

	// TMCC から有効 TSID を選択（reqTsid==0 の場合は先頭有効 TSID）
	uint8_t tmcc[24];
	demodRead(tunerFd, DEMOD_SAT_ADDR, 0xce, tmcc, sizeof(tmcc));
	uint16_t tsid = reqTsid;
	if (tsid == 0) {
		for (int i = 0; i < 12; i++) {
			uint16_t cand = static_cast<uint16_t>((tmcc[i * 2] << 8) | tmcc[i * 2 + 1]);
			if (cand != 0xffff && cand != 0x0000) {
				tsid = cand;
				break;
			}
		}
	}
	if (tsid != 0) {
		demodWrite(tunerFd, DEMOD_SAT_ADDR, 0x8f, static_cast<uint8_t>(tsid >> 8));
		demodWrite(tunerFd, DEMOD_SAT_ADDR, 0x90, static_cast<uint8_t>(tsid & 0xff));
		demodWrite(tunerFd, DEMOD_SAT_ADDR, 0x03, 0x01);
	}

	// スロットロック待ち (0xc5 & 0x03)
	for (int i = 0; i < 40; i++) {
		usleep(25 * 1000);
		uint8_t c5 = 0;
		demodRead(tunerFd, DEMOD_SAT_ADDR, 0xc5, &c5, 1);
		if (c5 & 0x03) break;
	}
}

void Mtvu3s::tuneTer(uint32_t freqKhz)
{
	// ISDB-S 側 TS ピンをトライステートにし、ISDB-T 側をアクティブ化 (0x0e=0x01)
	emRegWrite(tunerFd, 0x50, 0x01);
	demodWrite(tunerFd, DEMOD_SAT_ADDR, 0x1c, 0x80);
	demodWrite(tunerFd, DEMOD_SAT_ADDR, 0x1f, 0x22);
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x0e, 0x01);
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x0f, 0x77);
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x1d, 0x00);
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x1f, 0x00);

	// 選局前: Windows公式ドライバ準拠 (0x25=0x00, 0x23|=0x01)
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x25, 0x00);
	uint8_t r23 = 0xfc;
	demodRead(tunerFd, DEMOD_TER_ADDR, 0x23, &r23, 1);
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x23, static_cast<uint8_t>(r23 | 0x01));

	// FC0013 (0xc6) 選局 (emOEM64.sys 0x330e8 / 0x338de 準拠, xtal=28800 kHz, bw=6 MHz)
	const uint32_t xtalKhz = 28800;
	auto fcWrite = [&](uint8_t r, uint8_t v) {
		uint8_t msg[2] = { r, v };
		tunerWrite(tunerFd, DEMOD_TER_ADDR, TUNER_TER_ADDR, msg, 2);
	};
	auto fcRead = [&](uint8_t r) -> uint8_t {
		return tunerReadReg(tunerFd, DEMOD_TER_ADDR, TUNER_TER_ADDR, r);
	};

	fcWrite(0x1d, static_cast<uint8_t>(fcRead(0x1d) | 0x1c));
	fcWrite(0x07, static_cast<uint8_t>(fcRead(0x07) & 0xef));
	fcWrite(0x14, static_cast<uint8_t>((fcRead(0x14) & 0x1f) | (freqKhz <= 862000 ? 0x40 : 0x20)));

	uint32_t f = freqKhz;
	uint8_t multi = 4, r05 = 0x0a, r06 = 0x02;
	if      (f * 96 < 3560000) { multi = 96; r05 = 0x82; r06 = 0x00; }
	else if (f * 64 < 3560000) { multi = 64; r05 = 0x02; r06 = 0x02; }
	else if (f * 48 < 3560000) { multi = 48; r05 = 0x42; r06 = 0x00; }
	else if (f * 32 < 3560000) { multi = 32; r05 = 0x82; r06 = 0x02; }
	else if (f * 24 < 3560000) { multi = 24; r05 = 0x22; r06 = 0x00; }
	else if (f * 16 < 3560000) { multi = 16; r05 = 0x42; r06 = 0x02; }
	else if (f * 12 < 3560000) { multi = 12; r05 = 0x12; r06 = 0x00; }
	else if (f * 8  < 3560000) { multi = 8;  r05 = 0x22; r06 = 0x02; }
	else if (f * 6  < 3560000) { multi = 6;  r05 = 0x0a; r06 = 0x00; }
	else {
		multi = 4;
		if (f * 4 < 3800000) { r05 = 0x12; }
		else                 { f >>= 1; r05 = 0x0a; }
		r06 = 0x02;
	}
	r06 |= 0x08;
	uint8_t r09 = 0x4f;
	uint32_t fVco = f * multi;
	uint32_t xin = (fVco + xtalKhz / 2) / xtalKhz;
	uint32_t pm = xin >> 3;
	uint32_t am = xin - (pm << 3);
	if (am < 2) { am += 8; pm -= 1; }
	uint32_t rem = fVco - (fVco / xtalKhz) * xtalKhz;
	uint32_t k = (rem << 15) / xtalKhz;
	if (k >= 0x4000) k = (k + 0x8000) & 0xffff;
	if (pm < 11) {
		const uint32_t halfXtal = xtalKhz / 2;
		r09 = 0x6f;
		xin = (fVco + halfXtal / 2) / halfXtal;
		pm = xin >> 3;
		am = xin - (pm << 3);
		if (am < 2) { am += 8; pm -= 1; }
		rem = fVco - (fVco / halfXtal) * halfXtal;
		k = (rem << 15) / halfXtal;
		if (k >= 0x4000) k = (k + 0x8000) & 0xffff;
	}
	r06 |= 0x80; // bw = 6 MHz
	r05 |= 0x07;

	fcWrite(0x01, static_cast<uint8_t>(am & 0xff));
	fcWrite(0x02, static_cast<uint8_t>(pm & 0xff));
	fcWrite(0x03, static_cast<uint8_t>((k >> 8) & 0xff));
	fcWrite(0x04, static_cast<uint8_t>(k & 0xff));
	fcWrite(0x05, r05);
	fcWrite(0x06, r06);
	fcWrite(0x09, r09);
	uint8_t r11 = fcRead(0x11);
	fcWrite(0x11, static_cast<uint8_t>(multi == 0x40 ? (r11 | 0x04) : (r11 & 0xfb)));
	fcWrite(0x0e, 0x80);
	fcWrite(0x0e, 0x00);
	fcWrite(0x0e, 0x00);
	if ((fcRead(0x0e) & 0x3f) > 0x3c) {
		r06 &= 0xf7;
		fcWrite(0x06, r06);
		fcWrite(0x0e, 0x80);
		fcWrite(0x0e, 0x00);
	}
	fcWrite(0x08, 0x1f);
	fcWrite(0x0c, 0xf8);
	fcWrite(0x14, 0x50);

	// 選局後: Windows公式ドライバ準拠 (0x23&=~0x01, 0x25=0x80 -> 0x01=0x40)
	demodRead(tunerFd, DEMOD_TER_ADDR, 0x23, &r23, 1);
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x23, static_cast<uint8_t>(r23 & 0xfe));
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x25, 0x80);

	usleep(50 * 1000);
	demodWrite(tunerFd, DEMOD_TER_ADDR, 0x01, 0x40);
	for (int i = 0; i < 15; i++) {
		usleep(40 * 1000);
		adjustFc0013Lna(tunerFd);
		uint8_t st = 0xff;
		demodRead(tunerFd, DEMOD_TER_ADDR, 0x80, &st, 1);
		if (!(st & 0x28)) break;
	}
}

void Mtvu3s::setChannel(BandType band, int channel)
{
	assertInitialized();
	currentBand = band;

	if (band == BAND_BS) {
		uint32_t node = 0;
		uint16_t tsid = 0;
		if (channel >= 0x4000) {
			node = static_cast<uint32_t>((channel >> 4) & 0x1f);
			// recfriio.cpp の BS15_0 形式は下位4bitにスロット番号+1 (1..8) または 9 を入れる
			uint8_t tmcc[24];
			tsid = static_cast<uint16_t>(channel);
			(void)tmcc;
		} else if (1 <= channel && channel <= 12) {
			node = static_cast<uint32_t>(channel * 2 - 1);
		} else if (13 <= channel && channel <= 23 && (channel & 1)) {
			node = static_cast<uint32_t>(channel);
		} else {
			throw traceable_error("invalid BS channel.");
		}
		uint32_t freqKhz = 1049480 + ((node - 1) / 2) * 38360;
		// BSxx_y 指定で slot 指定が 0x40f9 等の場合は TMCC スロット順または直接 TSID で試す
		tuneSat(freqKhz, tsid);
	} else if (band == BAND_CS) {
		if (channel < 1 || channel > 12) {
			throw traceable_error("invalid CS channel.");
		}
		uint32_t freqKhz = 1613000 + static_cast<uint32_t>(channel - 1) * 40000;
		tuneSat(freqKhz, 0);
	} else if (band == BAND_UHF) {
		if (channel < 13 || channel > 62) {
			throw traceable_error("invalid UHF channel.");
		}
		uint32_t freqKhz = 473143 + static_cast<uint32_t>(channel - 13) * 6000;
		tuneTer(freqKhz);
	} else {
		throw traceable_error("unsupported band.");
	}
}

const float Mtvu3s::getSignalLevel(void)
{
	assertInitialized();
	if (currentBand == BAND_BS || currentBand == BAND_CS) {
		uint8_t b[2] = {};
		demodRead(tunerFd, DEMOD_SAT_ADDR, 0xbc, b, 2);
		uint16_t cndat = static_cast<uint16_t>((b[0] << 8) | b[1]);
		if (cndat < 3000) return 0.0f;
		double x = static_cast<double>(cndat - 3000);
		double p = sqrt(x) / 64.0;
		double p2 = p * p, p3 = p2 * p, p4 = p2 * p2, p5 = p4 * p;
		double cnr = (-1634.6 * p5 + 14341.0 * p4 - 50259.0 * p3 + 88977.0 * p2 - 89565.0 * p + 58857.0) / 1000.0;
		return cnr > 0.0 ? static_cast<float>(cnr) : 0.0f;
	}
	uint8_t b[3] = {};
	demodRead(tunerFd, DEMOD_TER_ADDR, 0x8b, b, 3);
	uint32_t cndat = (static_cast<uint32_t>(b[0]) << 16) | (static_cast<uint32_t>(b[1]) << 8) | b[2];
	if (cndat == 0) return 0.0f;
	return static_cast<float>(10.0 * log10(static_cast<double>(1 << 24) / cndat));
}

void Mtvu3s::startStream()
{
	assertInitialized();
	if (isStream) return;
	packBuf.clear();
	synced = false;
	usb_setinterface(tunerFd, TARGET_INTERFACE, TARGET_ALTSETTING_MTVU3S);
	emRegWrite(tunerFd, 0x5d, 0xef);
	emRegWrite(tunerFd, 0x5f, 0x89);
	AbstractFriio::startStream();
}

void Mtvu3s::stopStream()
{
	assertInitialized();
	if (!isStream) return;
	AbstractFriio::stopStream();
	emRegWrite(tunerFd, 0x5f, 0x08);
	usb_setinterface(tunerFd, TARGET_INTERFACE, TARGET_ALTSETTING);
}

int Mtvu3s::getStream(const uint8_t** bufp, int timeoutMsec)
{
	assertInitialized();
	if (!isStream) {
		startStream();
	}

	const AsyncRequest *req = ringBuf->getPopPtr(timeoutMsec);
	if (NULL == req) {
		*bufp = NULL;
		return 0;
	}
	const usbdevfs_urb *urb = req->urbPtr();
	if (urb->status != 0) {
		std::ostringstream stream;
		stream << "UrbStatus: " << urb->status;
		throw usb_error(stream.str());
	}

	for (int i = 0; i < urb->number_of_packets; i++) {
		const usbdevfs_iso_packet_desc &desc = urb->iso_frame_desc[i];
		if (desc.status == 0 && desc.actual_length > 0) {
			const uint8_t *src = req->buf + static_cast<size_t>(i) * isoPacketSize;
			packBuf.insert(packBuf.end(), src, src + desc.actual_length);
		}
	}

	if (!synced) {
		size_t limit = packBuf.size();
		size_t syncOff = limit;
		for (size_t i = 0; i + TS_PACKET_SIZE * 2 < limit; i++) {
			if (packBuf[i] == 0x47 &&
			    packBuf[i + TS_PACKET_SIZE] == 0x47 &&
			    packBuf[i + TS_PACKET_SIZE * 2] == 0x47) {
				syncOff = i;
				break;
			}
		}
		if (syncOff == limit) {
			if (packBuf.size() > TS_PACKET_SIZE * 3) {
				packBuf.erase(packBuf.begin(), packBuf.end() - TS_PACKET_SIZE * 3);
			}
			*bufp = outBuf.data();
			return 0;
		}
		if (syncOff > 0) {
			packBuf.erase(packBuf.begin(), packBuf.begin() + syncOff);
		}
		synced = true;
	}

	size_t nPkts = packBuf.size() / TS_PACKET_SIZE;
	if (nPkts == 0) {
		*bufp = outBuf.data();
		return 0;
	}

	size_t outBytes = nPkts * TS_PACKET_SIZE;
	outBuf.resize(outBytes);
	memcpy(outBuf.data(), packBuf.data(), outBytes);
	packBuf.erase(packBuf.begin(), packBuf.begin() + outBytes);

	// EM28178 AES-128-ECB 復号 (pkt[12..187], 11 ブロック)
	for (size_t p = 0; p < nPkts; p++) {
		uint8_t *pkt = outBuf.data() + p * TS_PACKET_SIZE;
		if (pkt[0] != 0x47) {
			synced = false;
		}
		for (size_t b = 0; b < TS_AES_BLOCKS; b++) {
			aes.decryptBlock(pkt + TS_AES_OFFSET + b * 16);
		}
	}

	*bufp = outBuf.data();
	return static_cast<int>(outBytes);
}
