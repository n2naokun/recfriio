// SPDX-License-Identifier: MIT
// Copyright (c) 2026 waya0125

/**
 * SKNET MonsterTV U3S (EM28178 + TC90522 x2 + FC0013 + QM1D1C0042)
 *
 * バス構成:
 *   USB (EM28178)
 *    +-- I2C 0x20: TC90522 (ISDB-T) --[0xfe]--> 0xc6: FC0013 (UHFチューナー)
 *    +-- I2C 0x22: TC90522 (ISDB-S) --[0xfe]--> 0xc0: QM1D1C0042 (BS/CSチューナー)
 */
#include <unistd.h>
#include <string.h>
#include <sstream>

#include "Mtvu3s.hpp"
#include "usbops.hpp"
#include "setting.hpp"
#include "error.hpp"

namespace {
// I2C アドレス
const uint8_t DEMOD_TER_ADDR = 0x20;
const uint8_t DEMOD_SAT_ADDR = 0x22;
const uint8_t TUNER_TER_ADDR = 0xc6;
const uint8_t TUNER_SAT_ADDR = 0xc0;

// EM28178 USB ベンダリクエスト
const uint8_t EM_REQ_REG      = 0x00; // EM28178 レジスタ R/W
const uint8_t EM_REQ_I2C_STOP = 0x02; // I2C R/W (STOP あり)
const uint8_t EM_REQ_I2C_CONT = 0x03; // I2C R/W (STOP なし / Repeated Start)

// EM28178 レジスタ・GPIO
const uint8_t EM_R06_I2C_CLK  = 0x06; // I2C クロック設定
const uint8_t EM_R0C_USBSUSP  = 0x0c; // サスペンド / 電源制御
const uint8_t EM_R50_TS_CFG   = 0x50; // TS 入力モード (ISDB-T 時 0x01)
const uint8_t EM_R5D_TS_CTRL  = 0x5d; // TS インターフェース制御
const uint8_t EM_R5F_TS_AES   = 0x5f; // TS1 有効 + 同期 + AES 鍵有効
const uint8_t EM_R60_AES_KEY  = 0x60; // AES-128-ECB 鍵 (0x60..0x6f)
const uint8_t EM_R73_SHADOW   = 0x73; // GPIO シャドウレジスタ
const uint8_t EM_R80_GPIO_P0  = 0x80; // GPIO ポート0 (bit1=電源LowON, bit7=リセット, bit0=復調)

// TS パケット・AES 定数
const size_t TS_PACKET_SIZE = 188;
const size_t TS_AES_OFFSET  = 12;
const size_t TS_AES_BLOCKS  = 11; // 11 * 16 = 176 バイト

// バンド別チャンネル計算定数 (kHz)
const uint32_t UHF_BASE_KHZ = 473143;
const uint32_t UHF_STEP_KHZ = 6000;
const uint32_t BS_BASE_KHZ  = 1049480;
const uint32_t BS_STEP_KHZ  = 38360;
const uint32_t CS_BASE_KHZ  = 1613000;
const uint32_t CS_STEP_KHZ  = 40000;
} // namespace

void Mtvu3s::Em28178I2cBus::write(uint8_t addr, const uint8_t *data, uint16_t len, bool stop)
{
	if (len > 256) {
		throw traceable_error("Em28178I2cBus::write length overflow.");
	}
	uint8_t buf[256];
	if (len > 0 && data != NULL) {
		memcpy(buf, data, len);
	}
	usbdevfs_ctrltransfer ctrl = {
		USB_DIR_OUT | USB_TYPE_VENDOR | USB_RECIP_DEVICE,
		stop ? EM_REQ_I2C_STOP : EM_REQ_I2C_CONT,
		0, addr, len, REQUEST_TIMEOUT, buf
	};
	usb_ctrl(fd, &ctrl);
}

void Mtvu3s::Em28178I2cBus::read(uint8_t addr, uint8_t *data, uint16_t len, bool stop)
{
	usbdevfs_ctrltransfer ctrl = {
		USB_DIR_IN | USB_TYPE_VENDOR | USB_RECIP_DEVICE,
		stop ? EM_REQ_I2C_STOP : EM_REQ_I2C_CONT,
		0, addr, len, REQUEST_TIMEOUT, data
	};
	usb_ctrl(fd, &ctrl);
}

Mtvu3s::Mtvu3s()
	: currentBand(BAND_UHF), synced(false),
	  terDemod(i2cBus, DEMOD_TER_ADDR), satDemod(i2cBus, DEMOD_SAT_ADDR),
	  terTuner(terDemod.tunerBus(), TUNER_TER_ADDR),
	  satTuner(satDemod.tunerBus(), TUNER_SAT_ADDR)
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
		EM_REQ_REG, 0, reg, 1, REQUEST_TIMEOUT, &val
	};
	usb_ctrl(fd, &ctrl);
	return val;
}

void Mtvu3s::emRegWrite(int fd, uint8_t reg, uint8_t val)
{
	usbdevfs_ctrltransfer ctrl = {
		USB_DIR_OUT | USB_TYPE_VENDOR | USB_RECIP_DEVICE,
		EM_REQ_REG, 0, reg, 1, REQUEST_TIMEOUT, &val
	};
	usb_ctrl(fd, &ctrl);
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

void Mtvu3s::UsbProcBegin(int fd)
{
	if (log) *log << "Mtvu3s UsbProcBegin..." << std::endl;
	i2cBus.setFd(fd);

	// EM28178 ウェイクアップ＆TC90522 リセット解除 (GPIO1=ON(Low), GPIO7=Low->High)
	emRegWrite(fd, EM_R0C_USBSUSP, 0x10);
	emRegWrite(fd, EM_R06_I2C_CLK, 0x44);
	emRegWrite(fd, EM_R80_GPIO_P0, 0x7c);
	usleep(50 * 1000);
	emRegWrite(fd, EM_R80_GPIO_P0, 0xfd);
	emRegWrite(fd, EM_R73_SHADOW,  0xfd);
	usleep(50 * 1000);

	terDemod.initTer();
	satDemod.initSat();
	satTuner.init();
	terTuner.init();

	// EM28178 AES-128-ECB 鍵 (全ゼロ) と TS キャプチャ設定
	for (uint8_t i = 0; i < 16; i++) {
		emRegWrite(fd, static_cast<uint8_t>(EM_R60_AES_KEY + i), 0x00);
	}
	emRegWrite(fd, EM_R5D_TS_CTRL, 0xef);
	uint8_t zeroKey[16] = {};
	aes.setKey(zeroKey);
}

void Mtvu3s::UsbProcEnd(int fd)
{
	if (log) *log << "Mtvu3s UsbProcEnd..." << std::endl;
	emRegWrite(fd, EM_R5F_TS_AES, 0x08);
	usb_setinterface(fd, TARGET_INTERFACE, TARGET_ALTSETTING);
}

void Mtvu3s::UsbProcStreamInit(int /*fd*/) {}
void Mtvu3s::UsbProcLED(int /*fd*/, BonLedColor /*color*/, bool /*lnb_powered*/) {}
void Mtvu3s::UsbProcFixInitB(int /*fd*/) {}

void Mtvu3s::tuneSat(uint32_t freqKhz, uint16_t reqTsid)
{
	terDemod.setTerActive(false);
	satDemod.setSatActive(true);
	satTuner.tune(freqKhz);

	satDemod.resetSat();
	waitUntil([&]() { return satDemod.isSatTmccLocked(); }, 40, 25);

	satDemod.selectSatTsid(reqTsid);
	waitUntil([&]() { return satDemod.isSatSlotLocked(); }, 40, 25);
}

void Mtvu3s::tuneTer(uint32_t freqKhz)
{
	emRegWrite(tunerFd, EM_R50_TS_CFG, 0x01);
	satDemod.setSatActive(false);
	terDemod.setTerActive(true);

	terDemod.holdAgcForTune(true);
	terTuner.tune(freqKhz, 6);
	terDemod.holdAgcForTune(false);

	usleep(50 * 1000);
	terDemod.resetTer();
	waitUntil([&]() {
		terTuner.adjustLna();
		return terDemod.isTerLocked();
	}, 15, 40);
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
			tsid = static_cast<uint16_t>(channel);
		} else if (1 <= channel && channel <= 12) {
			node = static_cast<uint32_t>(channel * 2 - 1);
		} else if (13 <= channel && channel <= 23 && (channel & 1)) {
			node = static_cast<uint32_t>(channel);
		} else {
			throw traceable_error("invalid BS channel.");
		}
		uint32_t freqKhz = BS_BASE_KHZ + ((node - 1) / 2) * BS_STEP_KHZ;
		tuneSat(freqKhz, tsid);
	} else if (band == BAND_CS) {
		if (channel < 1 || channel > 12) {
			throw traceable_error("invalid CS channel.");
		}
		uint32_t freqKhz = CS_BASE_KHZ + static_cast<uint32_t>(channel - 1) * CS_STEP_KHZ;
		tuneSat(freqKhz, 0);
	} else if (band == BAND_UHF) {
		if (channel < 13 || channel > 62) {
			throw traceable_error("invalid UHF channel.");
		}
		uint32_t freqKhz = UHF_BASE_KHZ + static_cast<uint32_t>(channel - 13) * UHF_STEP_KHZ;
		tuneTer(freqKhz);
	} else {
		throw traceable_error("unsupported band.");
	}
}

const float Mtvu3s::getSignalLevel(void)
{
	assertInitialized();
	if (currentBand == BAND_BS || currentBand == BAND_CS) {
		return satDemod.getSatCnr();
	}
	return terDemod.getTerCnr();
}

void Mtvu3s::startStream()
{
	assertInitialized();
	if (isStream) return;
	packBuf.clear();
	synced = false;
	usb_setinterface(tunerFd, TARGET_INTERFACE, TARGET_ALTSETTING_MTVU3S);
	emRegWrite(tunerFd, EM_R5D_TS_CTRL, 0xef);
	emRegWrite(tunerFd, EM_R5F_TS_AES, 0x89);
	AbstractFriio::startStream();
}

void Mtvu3s::stopStream()
{
	assertInitialized();
	if (!isStream) return;
	AbstractFriio::stopStream();
	emRegWrite(tunerFd, EM_R5F_TS_AES, 0x08);
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
