package linux

import (
	"fmt"
	"io"
	"log"

	"github.com/paypal/gatt/linux/internal/cmd"
	"github.com/paypal/gatt/linux/internal/device"
	"github.com/paypal/gatt/linux/internal/event"
	"github.com/paypal/gatt/linux/internal/l2cap"
)

type PacketType uint8

// HCI Packet types
const (
	ptypeCommandPkt PacketType = 0X01
	ptypeACLDataPkt            = 0X02
	ptypeSCODataPkt            = 0X03
	ptypeEventPkt              = 0X04
	ptypeVendorPkt             = 0XFF
)

type HCI struct {
	dev    io.ReadWriteCloser
	logger *log.Logger
	cmd    *cmd.Cmd
	evt    *event.Event
	l2c    *l2cap.L2CAP
}

func (h HCI) Cmd() *cmd.Cmd       { return h.cmd }
func (h HCI) Event() *event.Event { return h.evt }
func (h HCI) L2CAP() *l2cap.L2CAP { return h.l2c }

func NewHCI(l *log.Logger, maxConn int) *HCI {
	d, err := device.NewSocket(1)
	if err != nil {
		d, err = device.NewSocket(0)
		if err != nil {
			return nil
		}
	}
	c := cmd.NewCmd(d, l)
	l2c := l2cap.NewL2CAP(c, d, l, maxConn)
	e := event.NewEvent(l)
	h := &HCI{
		dev: d,
		cmd: c,
		evt: e,
		l2c: l2c,
	}

	e.HandleEvent(event.LEMeta, event.HandlerFunc(l2c.HandleLEMeta))
	e.HandleEvent(event.DisconnectionComplete, event.HandlerFunc(l2c.HandleDisconnectionComplete))
	e.HandleEvent(event.NumberOfCompletedPkts, event.HandlerFunc(l2c.HandleNumberOfCompletedPkts))
	e.HandleEvent(event.CommandComplete, event.HandlerFunc(c.HandleComplete))
	e.HandleEvent(event.CommandStatus, event.HandlerFunc(c.HandleStatus))

	return h
}

func (h HCI) Close() error {
	return h.dev.Close()
}

func (h HCI) Start() error {
	go h.mainLoop()
	return h.ResetDevice()
}

func (h HCI) mainLoop() {
	b := make([]byte, 4096)
	for {
		n, err := h.dev.Read(b)
		if err != nil {
			log.Printf("Failed to Read: %s", err)
			return
		}
		if n == 0 {
			log.Printf("Dev Read 0 byte. fd had been closed")
			return
		}
		p := make([]byte, n)
		copy(p, b)
		go h.handlePacket(p)
	}
}

func (h HCI) handlePacket(b []byte) {
	t, b := PacketType(b[0]), b[1:]
	var err error
	switch t {
	case ptypeCommandPkt:
		err = h.handleCmd(b)
	case ptypeACLDataPkt:
		err = h.l2c.HandleL2CAP(b)
	case ptypeSCODataPkt:
		err = h.handleSCO(b)
	case ptypeEventPkt:
		err = h.evt.Dispatch(b)
	case ptypeVendorPkt:
		err = h.handleVendor(b)
	default:
		log.Fatalf("Unknown Event: 0x%02X [ % X ]\n", t, b)
	}
	if err != nil {
		log.Printf("hci: %s, [ % X]", err, b)
	}
}

func (h HCI) handleCmd(b []byte) error {
	// This is most likely command generated by Linux kernel.
	// In this case, we need to find a way to tell kernel not to touch the device.
	op := uint16(b[0]) | uint16(b[1])<<8
	log.Printf("unmanaged cmd: %s(0x%04X)\n", cmd.Opcode(op), op)
	return nil
}

func (h HCI) handleSCO(b []byte) error {
	return fmt.Errorf("SCO packet not supported")
}

func (h HCI) handleVendor(b []byte) error {
	return fmt.Errorf("Vendor packet not supported")
}

type cmdSeq struct {
	cp  cmd.CmdParam
	exp []byte
}

var expSuccess = []byte{0x00}

var bcmResetSeq = []cmdSeq{
	{cmd.WriteSimplePairingMode{SimplePairingMode: 1}, expSuccess},
	{cmd.WriteLEHostSupported{LESupportedHost: 1, SimultaneousLEHost: 0}, expSuccess},
	// {cmd.SetEventMaskPage2{0x00000000007FC000}, expSuccess},
	{cmd.WriteInquiryMode{InquiryMode: 2}, expSuccess},
	{cmd.WritePageScanType{PageScanType: 1}, expSuccess},
	{cmd.WriteInquiryScanType{ScanType: 1}, expSuccess},
	{cmd.WriteClassOfDevice{ClassOfDevice: [3]byte{0x40, 0x02, 0x04}}, expSuccess},
	{cmd.WritePageTimeout{PageTimeout: 0x2000}, expSuccess},
	{cmd.WriteDefaultLinkPolicy{DefaultLinkPolicySettings: 0x5}, expSuccess},
	{cmd.HostBufferSize{
		HostACLDataPacketLength:            0x1000,
		HostSynchronousDataPacketLength:    0xff,
		HostTotalNumACLDataPackets:         0x0014,
		HostTotalNumSynchronousDataPackets: 0x000a,
	}, expSuccess},
}

var defaultResetSeq = []cmdSeq{
	{cmd.Reset{}, expSuccess},
	{cmd.SetEventMask{EventMask: 0x3dbff807fffbffff}, expSuccess},
	// {cmd.SetEventFlt{0x0, 0x00, 0x00}, expSuccess},
	{cmd.LESetEventMask{LEEventMask: 0x000000000000001F}, expSuccess},
}

func (h HCI) ResetDevice() error {
	for _, s := range defaultResetSeq {
		if err := h.Cmd().SendAndCheckResp(s.cp, s.exp); err != nil {
			return err
		}
	}
	for _, s := range bcmResetSeq {
		if err := h.Cmd().SendAndCheckResp(s.cp, s.exp); err != nil {
			return err
		}
	}
	return nil
}
