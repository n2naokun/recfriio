// MonsterTV U3S (SK-MTVU3S, USB 3275:9015)

#ifndef _MTVU3S_HPP_
#define _MTVU3S_HPP_

#include <vector>
#include "AbstractFriio.hpp"
#include "Aes128Ecb.hpp"

/**
 * SKNET MonsterTV U3S 用チューナクラス
 */
class Mtvu3s : public AbstractFriio
{
public:
	Mtvu3s();
	virtual ~Mtvu3s();

	virtual void setChannel(BandType band, int channel);
	virtual const float getSignalLevel(void);
	virtual void startStream();
	virtual void stopStream();
	virtual int getStream(const uint8_t** bufp, int timeoutMsec);

protected:
	virtual bool is_friio(const std::string &devfile);
	virtual TunerType UsbProcFixInitA(int fd);
	virtual void UsbProcBegin(int fd);
	virtual void UsbProcEnd(int fd);
	virtual void UsbProcStreamInit(int fd);
	virtual void UsbProcLED(int fd, BonLedColor color, bool lnb_powered);
	virtual void UsbProcFixInitB(int fd);

private:
	BandType currentBand;
	Aes128EcbDecryptor aes;
	uint8_t qmRegs[32];
	std::vector<uint8_t> packBuf;
	std::vector<uint8_t> outBuf;
	bool synced;

	uint8_t emRegRead(int fd, uint8_t reg);
	void emRegWrite(int fd, uint8_t reg, uint8_t val);
	void i2cWrite(int fd, uint8_t addr, const uint8_t *data, uint16_t len, bool stop = true);
	void i2cRead(int fd, uint8_t addr, uint8_t *data, uint16_t len, bool stop = true);

	void demodWrite(int fd, uint8_t addr, uint8_t reg, uint8_t val);
	void demodRead(int fd, uint8_t addr, uint8_t reg, uint8_t *buf, uint16_t len);
	void tunerWrite(int fd, uint8_t demodAddr, uint8_t tunerAddr, const uint8_t *data, uint16_t len);
	uint8_t tunerReadReg(int fd, uint8_t demodAddr, uint8_t tunerAddr, uint8_t reg);

	void initDemods(int fd);
	void initQm1d1c0042(int fd);
	void initFc0013(int fd);
	void adjustFc0013Lna(int fd);
	void tuneSat(uint32_t freqKhz, uint16_t reqTsid);
	void tuneTer(uint32_t freqKhz);
};

#endif
