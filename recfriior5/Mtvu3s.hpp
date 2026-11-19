#ifndef _MTVU3S_HPP_
#define _MTVU3S_HPP_

#include <vector>
#include <cstdint>
#include "AbstractFriio.hpp"
#include "Aes128Ecb.hpp"
#include "I2cBus.hpp"
#include "Tc90522.hpp"
#include "Qm1d1c0042.hpp"
#include "Fc0013.hpp"

/**
 * SKNET MonsterTV U3S (USB 3275:9015, EM28178 + TC90522 x2 + QM1D1C0042 + FC0013)
 */
class Mtvu3s : public AbstractFriio {
public:
	Mtvu3s();
	virtual ~Mtvu3s();

	virtual void setChannel(BandType band, int channel);
	virtual const float getSignalLevel(void);
	virtual int getStream(const uint8_t** bufp, int timeoutMsec);
	virtual void startStream();
	virtual void stopStream();

protected:
	virtual bool is_friio(const std::string &devfile);
	virtual TunerType UsbProcFixInitA(int fd);
	virtual void UsbProcBegin(int fd);
	virtual void UsbProcEnd(int fd);
	virtual void UsbProcStreamInit(int fd);
	virtual void UsbProcLED(int fd, BonLedColor color, bool lnb_powered);
	virtual void UsbProcFixInitB(int fd);

private:
	class Em28178I2cBus : public I2cBus {
	public:
		Em28178I2cBus() : fd(-1) {}
		void setFd(int devFd) { fd = devFd; }
		virtual void write(uint8_t addr, const uint8_t *data, uint16_t len, bool stop = true);
		virtual void read(uint8_t addr, uint8_t *data, uint16_t len, bool stop = true);

	private:
		int fd;
	};

	BandType currentBand;
	Aes128EcbDecryptor aes;
	std::vector<uint8_t> packBuf;
	std::vector<uint8_t> outBuf;
	bool synced;

	Em28178I2cBus i2cBus;
	Tc90522 terDemod;
	Tc90522 satDemod;
	Fc0013 terTuner;
	Qm1d1c0042 satTuner;

	uint8_t emRegRead(int fd, uint8_t reg);
	void emRegWrite(int fd, uint8_t reg, uint8_t val);

	void tuneSat(uint32_t freqKhz, uint16_t reqTsid);
	void tuneTer(uint32_t freqKhz);
};

#endif
