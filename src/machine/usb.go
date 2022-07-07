//go:build sam || nrf52840
// +build sam nrf52840

package machine

import (
	"errors"
	"runtime/volatile"
)

type USBDevice struct {
	initcomplete bool
}

var (
	USBDev = &USBDevice{}
)
var usbDescriptor = descriptorCDC

var (
	errUSBCDCBufferEmpty      = errors.New("USB-CDC buffer empty")
	errUSBCDCWriteByteTimeout = errors.New("USB-CDC write byte timeout")
	errUSBCDCReadTimeout      = errors.New("USB-CDC read timeout")
	errUSBCDCBytesRead        = errors.New("USB-CDC invalid number of bytes read")
)

const cdcLineInfoSize = 7

type cdcLineInfo struct {
	dwDTERate   uint32
	bCharFormat uint8
	bParityType uint8
	bDataBits   uint8
	lineState   uint8
}

// strToUTF16LEDescriptor converts a utf8 string into a string descriptor
// note: the following code only converts ascii characters to UTF16LE. In order
// to do a "proper" conversion, we would need to pull in the 'unicode/utf16'
// package, which at the time this was written added 512 bytes to the compiled
// binary.
func strToUTF16LEDescriptor(in string, out []byte) {
	out[0] = byte(len(out))
	out[1] = usb_STRING_DESCRIPTOR_TYPE
	for i, rune := range in {
		out[(i<<1)+2] = byte(rune)
		out[(i<<1)+3] = 0
	}
	return
}

var (
	// TODO: allow setting these
	usb_STRING_LANGUAGE = [2]uint16{(3 << 8) | (2 + 2), 0x0409} // English
)

var (
	usbEndpointDescriptors [8]usbDeviceDescriptor

	udd_ep_in_cache_buffer  [7][128]uint8
	udd_ep_out_cache_buffer [7][128]uint8

	isEndpointHalt        = false
	isRemoteWakeUpEnabled = false
	endPoints             = []uint32{usb_ENDPOINT_TYPE_CONTROL,
		(usb_ENDPOINT_TYPE_INTERRUPT | usbEndpointIn),
		(usb_ENDPOINT_TYPE_BULK | usbEndpointOut),
		(usb_ENDPOINT_TYPE_BULK | usbEndpointIn)}

	usbConfiguration uint8
	usbSetInterface  uint8
	usbLineInfo      = cdcLineInfo{115200, 0x00, 0x00, 0x08, 0x00}
)

const (
	usb_IMANUFACTURER = 1
	usb_IPRODUCT      = 2
	usb_ISERIAL       = 3

	usb_ENDPOINT_TYPE_CONTROL     = 0x00
	usb_ENDPOINT_TYPE_ISOCHRONOUS = 0x01
	usb_ENDPOINT_TYPE_BULK        = 0x02
	usb_ENDPOINT_TYPE_INTERRUPT   = 0x03

	usb_DEVICE_DESCRIPTOR_TYPE        = 1
	usb_CONFIGURATION_DESCRIPTOR_TYPE = 2
	usb_STRING_DESCRIPTOR_TYPE        = 3
	usb_INTERFACE_DESCRIPTOR_TYPE     = 4
	usb_ENDPOINT_DESCRIPTOR_TYPE      = 5
	usb_DEVICE_QUALIFIER              = 6
	usb_OTHER_SPEED_CONFIGURATION     = 7
	usb_SET_REPORT_TYPE               = 33
	usb_HID_REPORT_TYPE               = 34

	usbEndpointOut = 0x00
	usbEndpointIn  = 0x80

	usbEndpointPacketSize = 64 // 64 for Full Speed, EPT size max is 1024
	usb_EPT_NUM           = 7

	// standard requests
	usb_GET_STATUS        = 0
	usb_CLEAR_FEATURE     = 1
	usb_SET_FEATURE       = 3
	usb_SET_ADDRESS       = 5
	usb_GET_DESCRIPTOR    = 6
	usb_SET_DESCRIPTOR    = 7
	usb_GET_CONFIGURATION = 8
	usb_SET_CONFIGURATION = 9
	usb_GET_INTERFACE     = 10
	usb_SET_INTERFACE     = 11

	// non standard requests
	usb_SET_IDLE = 10

	usb_DEVICE_CLASS_COMMUNICATIONS  = 0x02
	usb_DEVICE_CLASS_HUMAN_INTERFACE = 0x03
	usb_DEVICE_CLASS_STORAGE         = 0x08
	usb_DEVICE_CLASS_VENDOR_SPECIFIC = 0xFF

	usb_CONFIG_POWERED_MASK  = 0x40
	usb_CONFIG_BUS_POWERED   = 0x80
	usb_CONFIG_SELF_POWERED  = 0xC0
	usb_CONFIG_REMOTE_WAKEUP = 0x20

	// CDC
	usb_CDC_ACM_INTERFACE  = 0 // CDC ACM
	usb_CDC_DATA_INTERFACE = 1 // CDC Data
	usb_CDC_FIRST_ENDPOINT = 1

	// Endpoint
	usb_CDC_ENDPOINT_ACM = 1
	usb_CDC_ENDPOINT_OUT = 2
	usb_CDC_ENDPOINT_IN  = 3
	usb_HID_ENDPOINT_IN  = 4

	// bmRequestType
	usb_REQUEST_HOSTTODEVICE = 0x00
	usb_REQUEST_DEVICETOHOST = 0x80
	usb_REQUEST_DIRECTION    = 0x80

	usb_REQUEST_STANDARD = 0x00
	usb_REQUEST_CLASS    = 0x20
	usb_REQUEST_VENDOR   = 0x40
	usb_REQUEST_TYPE     = 0x60

	usb_REQUEST_DEVICE    = 0x00
	usb_REQUEST_INTERFACE = 0x01
	usb_REQUEST_ENDPOINT  = 0x02
	usb_REQUEST_OTHER     = 0x03
	usb_REQUEST_RECIPIENT = 0x1F

	usb_REQUEST_DEVICETOHOST_CLASS_INTERFACE    = (usb_REQUEST_DEVICETOHOST | usb_REQUEST_CLASS | usb_REQUEST_INTERFACE)
	usb_REQUEST_HOSTTODEVICE_CLASS_INTERFACE    = (usb_REQUEST_HOSTTODEVICE | usb_REQUEST_CLASS | usb_REQUEST_INTERFACE)
	usb_REQUEST_DEVICETOHOST_STANDARD_INTERFACE = (usb_REQUEST_DEVICETOHOST | usb_REQUEST_STANDARD | usb_REQUEST_INTERFACE)

	// CDC Class requests
	usb_CDC_SET_LINE_CODING        = 0x20
	usb_CDC_GET_LINE_CODING        = 0x21
	usb_CDC_SET_CONTROL_LINE_STATE = 0x22
	usb_CDC_SEND_BREAK             = 0x23

	usb_CDC_V1_10                         = 0x0110
	usb_CDC_COMMUNICATION_INTERFACE_CLASS = 0x02

	usb_CDC_CALL_MANAGEMENT             = 0x01
	usb_CDC_ABSTRACT_CONTROL_MODEL      = 0x02
	usb_CDC_HEADER                      = 0x00
	usb_CDC_ABSTRACT_CONTROL_MANAGEMENT = 0x02
	usb_CDC_UNION                       = 0x06
	usb_CDC_CS_INTERFACE                = 0x24
	usb_CDC_CS_ENDPOINT                 = 0x25
	usb_CDC_DATA_INTERFACE_CLASS        = 0x0A

	usb_CDC_LINESTATE_DTR = 0x01
	usb_CDC_LINESTATE_RTS = 0x02
)

// usbDeviceDescBank is the USB device endpoint descriptor.
type usbDeviceDescBank struct {
	ADDR      volatile.Register32
	PCKSIZE   volatile.Register32
	EXTREG    volatile.Register16
	STATUS_BK volatile.Register8
	_reserved [5]volatile.Register8
}

type usbDeviceDescriptor struct {
	DeviceDescBank [2]usbDeviceDescBank
}

type USBSetup struct {
	BmRequestType uint8
	BRequest      uint8
	WValueL       uint8
	WValueH       uint8
	WIndex        uint16
	WLength       uint16
}

func newUSBSetup(data []byte) USBSetup {
	u := USBSetup{}
	u.BmRequestType = uint8(data[0])
	u.BRequest = uint8(data[1])
	u.WValueL = uint8(data[2])
	u.WValueH = uint8(data[3])
	u.WIndex = uint16(data[4]) | (uint16(data[5]) << 8)
	u.WLength = uint16(data[6]) | (uint16(data[7]) << 8)
	return u
}

// USBCDC is the serial interface that works over the USB port.
// To implement the USBCDC interface for a board, you must declare a concrete type as follows:
//
// 		type USBCDC struct {
// 			Buffer *RingBuffer
// 		}
//
// You can also add additional members to this struct depending on your implementation,
// but the *RingBuffer is required.
// When you are declaring the USBCDC for your board, make sure that you also declare the
// RingBuffer using the NewRingBuffer() function:
//
//		USBCDC{Buffer: NewRingBuffer()}
//

// Read from the RX buffer.
func (usbcdc *USBCDC) Read(data []byte) (n int, err error) {
	// check if RX buffer is empty
	size := usbcdc.Buffered()
	if size == 0 {
		return 0, nil
	}

	// Make sure we do not read more from buffer than the data slice can hold.
	if len(data) < size {
		size = len(data)
	}

	// only read number of bytes used from buffer
	for i := 0; i < size; i++ {
		v, _ := usbcdc.ReadByte()
		data[i] = v
	}

	return size, nil
}

// Write data to the USBCDC.
func (usbcdc *USBCDC) Write(data []byte) (n int, err error) {
	for _, v := range data {
		usbcdc.WriteByte(v)
	}
	return len(data), nil
}

// ReadByte reads a single byte from the RX buffer.
// If there is no data in the buffer, returns an error.
func (usbcdc *USBCDC) ReadByte() (byte, error) {
	// check if RX buffer is empty
	buf, ok := usbcdc.Buffer.Get()
	if !ok {
		return 0, errUSBCDCBufferEmpty
	}
	return buf, nil
}

// Buffered returns the number of bytes currently stored in the RX buffer.
func (usbcdc *USBCDC) Buffered() int {
	return int(usbcdc.Buffer.Used())
}

// Receive handles adding data to the UART's data buffer.
// Usually called by the IRQ handler for a machine.
func (usbcdc *USBCDC) Receive(data byte) {
	usbcdc.Buffer.Put(data)
}

// sendDescriptor creates and sends the various USB descriptor types that
// can be requested by the host.
func sendDescriptor(setup USBSetup) {
	switch setup.WValueH {
	case usb_CONFIGURATION_DESCRIPTOR_TYPE:
		sendUSBPacket(0, usbDescriptor.Configuration, setup.WLength)
		return
	case usb_DEVICE_DESCRIPTOR_TYPE:
		// composite descriptor
		usbDescriptor.Configure(usb_VID, usb_PID)
		sendUSBPacket(0, usbDescriptor.Device, setup.WLength)
		return

	case usb_STRING_DESCRIPTOR_TYPE:
		switch setup.WValueL {
		case 0:
			b := []byte{0x04, 0x03, 0x09, 0x04}
			sendUSBPacket(0, b, setup.WLength)

		case usb_IPRODUCT:
			b := make([]byte, (len(usb_STRING_PRODUCT)<<1)+2)
			strToUTF16LEDescriptor(usb_STRING_PRODUCT, b)
			sendUSBPacket(0, b, setup.WLength)

		case usb_IMANUFACTURER:
			b := make([]byte, (len(usb_STRING_MANUFACTURER)<<1)+2)
			strToUTF16LEDescriptor(usb_STRING_MANUFACTURER, b)
			sendUSBPacket(0, b, setup.WLength)

		case usb_ISERIAL:
			// TODO: allow returning a product serial number
			SendZlp()
		}
		return
	case usb_HID_REPORT_TYPE:
		if h, ok := usbDescriptor.HID[setup.WIndex]; ok {
			sendUSBPacket(0, h, setup.WLength)
			return
		}
	case usb_DEVICE_QUALIFIER:
		// skip
	default:
	}

	// do not know how to handle this message, so return zero
	SendZlp()
	return
}

func handleStandardSetup(setup USBSetup) bool {
	switch setup.BRequest {
	case usb_GET_STATUS:
		buf := []byte{0, 0}

		if setup.BmRequestType != 0 { // endpoint
			if isEndpointHalt {
				buf[0] = 1
			}
		}

		sendUSBPacket(0, buf, setup.WLength)
		return true

	case usb_CLEAR_FEATURE:
		if setup.WValueL == 1 { // DEVICEREMOTEWAKEUP
			isRemoteWakeUpEnabled = false
		} else if setup.WValueL == 0 { // ENDPOINTHALT
			isEndpointHalt = false
		}
		SendZlp()
		return true

	case usb_SET_FEATURE:
		if setup.WValueL == 1 { // DEVICEREMOTEWAKEUP
			isRemoteWakeUpEnabled = true
		} else if setup.WValueL == 0 { // ENDPOINTHALT
			isEndpointHalt = true
		}
		SendZlp()
		return true

	case usb_SET_ADDRESS:
		return handleUSBSetAddress(setup)

	case usb_GET_DESCRIPTOR:
		sendDescriptor(setup)
		return true

	case usb_SET_DESCRIPTOR:
		return false

	case usb_GET_CONFIGURATION:
		buff := []byte{usbConfiguration}
		sendUSBPacket(0, buff, setup.WLength)
		return true

	case usb_SET_CONFIGURATION:
		if setup.BmRequestType&usb_REQUEST_RECIPIENT == usb_REQUEST_DEVICE {
			for i := 1; i < len(endPoints); i++ {
				initEndpoint(uint32(i), endPoints[i])
			}

			usbConfiguration = setup.WValueL

			SendZlp()
			return true
		} else {
			return false
		}

	case usb_GET_INTERFACE:
		buff := []byte{usbSetInterface}
		sendUSBPacket(0, buff, setup.WLength)
		return true

	case usb_SET_INTERFACE:
		usbSetInterface = setup.WValueL

		SendZlp()
		return true

	default:
		return true
	}
}

// EnableHID enables HID. This function must be executed from the init().
func EnableHID(callback func()) {
	usbDescriptor = descriptorCDCHID
	endPoints = []uint32{usb_ENDPOINT_TYPE_CONTROL,
		(usb_ENDPOINT_TYPE_INTERRUPT | usbEndpointIn),
		(usb_ENDPOINT_TYPE_BULK | usbEndpointOut),
		(usb_ENDPOINT_TYPE_BULK | usbEndpointIn),
		(usb_ENDPOINT_TYPE_INTERRUPT | usbEndpointIn)}

	hidCallback = callback
}

// hidCallback is a variable that holds the callback when using HID.
var hidCallback func()
