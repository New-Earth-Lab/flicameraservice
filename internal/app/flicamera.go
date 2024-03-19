package app

/*
extern void imageReceived(void*, void*);
*/
import "C"
import (
	"context"
	"fmt"
	"runtime/cgo"
	"strings"
	"time"
	"unsafe"

	"github.com/New-Earth-Lab/flisdk-go/flisdk"
	"github.com/lirm/aeron-go/aeron"
	"github.com/lirm/aeron-go/aeron/atomic"
	"github.com/lirm/aeron-go/aeron/flyweight"
	"github.com/lirm/aeron-go/aeron/util"
	"golang.org/x/sync/errgroup"
)

type FLICamera struct {
	callbackHandler    flisdk.CallbackHandler
	sdk                *flisdk.FliSdk
	publication        *aeron.Publication
	imageBuffer        *atomic.Buffer
	headerBuffer       *atomic.Buffer
	metadataBuffer     *atomic.Buffer
	handle             cgo.Handle
	header             ImageHeader
	headerBufferLength int
}

const (
	RingBufferNumImages = 4
)

type FliConfig struct {
	Width        uint32
	Height       uint32
	OffsetX      uint16
	OffsetY      uint16
	SerialNumber string
}

func NewFliCamera(config FliConfig, publication *aeron.Publication) (*FLICamera, error) {
	// Get the configuration for the camera
	// Grabber name
	// Camera name
	// ROI - cropping mode
	// Gain

	var err error
	sdk, err := flisdk.Init()
	if err != nil {
		return nil, err
	}
	defer func(e *error) {
		if *e != nil {
			sdk.Exit()
		}
	}(&err)

	// Get list of grabbers
	_, err = sdk.DetectGrabbers()
	if err != nil {
		return nil, err
	}

	// Get list of cameras
	cameraStrings, err := sdk.DetectCameras()
	if err != nil {
		return nil, err
	}

	found := false
	for _, cam := range cameraStrings {
		if strings.Contains(cam, config.SerialNumber) {
			err = sdk.SetCamera(cam)
			if err != nil {
				return nil, err
			}
			found = true
			break
		}
	}

	// Set the camera to the configured model if found
	if !found {
		return nil, fmt.Errorf("flicamera: Unable to find camera: %s", config.SerialNumber)
	}

	err = sdk.SetMode(flisdk.Mode_Full)
	if err != nil {
		return nil, err
	}

	err = sdk.Update()
	if err != nil {
		return nil, err
	}

	// Set sensor cropping
	croppingData := flisdk.CroppingData{
		Col1:    config.OffsetX,
		Col2:    config.OffsetX + uint16(config.Width) - 1,
		Row1:    config.OffsetY,
		Row2:    config.OffsetY + uint16(config.Height) - 1,
		Enabled: true,
	}

	err = sdk.SetCroppingState(croppingData)
	if err != nil {
		return nil, err
	}

	// Set the pixel format to unsigned
	sdk.EnableUnsignedPixel(true)

	// Enable the ring buffer to shrink it
	sdk.EnableRingBuffer(true)
	sdk.SetBufferSizeInImages(RingBufferNumImages)

	// Disable the ring buffer
	sdk.EnableRingBuffer(false)
	sdk.SetNumberImagesPerBuffer(1)

	// Get image dimensions for buffer size
	width, height := sdk.GetCurrentImageDimension()

	cam := FLICamera{
		sdk:          sdk,
		imageBuffer:  new(atomic.Buffer),
		publication:  publication,
		headerBuffer: atomic.MakeBuffer(make([]byte, 256)), // TODO: this is wasteful
	}

	// Set static header information
	cam.header.Wrap(cam.headerBuffer, 0)
	cam.header.Version.Set(0)
	cam.header.PayloadType.Set(0)
	cam.header.Format.Set(0x01100007) // Mono16
	cam.header.SizeX.Set(int32(width))
	cam.header.SizeY.Set(int32(height))
	cam.header.OffsetX.Set(0)
	cam.header.OffsetY.Set(0)
	cam.header.PaddingX.Set(0)
	cam.header.PaddingY.Set(0)
	cam.header.MetadataLength.Set(0)
	cam.header.ImageBufferLength.Set(int32(sdk.GetImageSizeInBytes()))

	cam.callbackHandler = sdk.AddCallbackNewImage(
		(flisdk.NewImageAvailableCallBack)(C.imageReceived),
		0, true, cam)

	return &cam, nil
}

func (f *FLICamera) StartCamera() error {
	return f.sdk.Start()
}

func (f *FLICamera) StopCamera() error {
	return f.sdk.Stop()
}

func (f *FLICamera) Shutdown() error {
	err := f.sdk.Stop()
	if err != nil {
		return err
	}

	f.sdk.RemoveCallbackNewImage(f.callbackHandler)
	f.sdk.Exit()

	return nil
}

func (f *FLICamera) Run(ctx context.Context) error {
	wg, ctx := errgroup.WithContext(ctx)

	wg.Go(func() error {
		<-ctx.Done()
		return f.Shutdown()
	})
	return wg.Wait()
}

type ImageHeader struct {
	flyweight.FWBase

	Version           flyweight.Int32Field
	PayloadType       flyweight.Int32Field
	TimestampNs       flyweight.Int64Field
	Format            flyweight.Int32Field
	SizeX             flyweight.Int32Field
	SizeY             flyweight.Int32Field
	OffsetX           flyweight.Int32Field
	OffsetY           flyweight.Int32Field
	PaddingX          flyweight.Int32Field
	PaddingY          flyweight.Int32Field
	MetadataLength    flyweight.Int32Field
	MetadataBuffer    flyweight.RawDataField
	pad0              flyweight.Padding
	ImageBufferLength flyweight.Int32Field
}

func (m *ImageHeader) Wrap(buf *atomic.Buffer, offset int) flyweight.Flyweight {
	pos := offset
	pos += m.Version.Wrap(buf, pos)
	pos += m.PayloadType.Wrap(buf, pos)
	pos += m.TimestampNs.Wrap(buf, pos)
	pos += m.Format.Wrap(buf, pos)
	pos += m.SizeX.Wrap(buf, pos)
	pos += m.SizeY.Wrap(buf, pos)
	pos += m.OffsetX.Wrap(buf, pos)
	pos += m.OffsetY.Wrap(buf, pos)
	pos += m.PaddingX.Wrap(buf, pos)
	pos += m.PaddingY.Wrap(buf, pos)
	pos += m.MetadataLength.Wrap(buf, pos)
	pos += m.MetadataBuffer.Wrap(buf, pos, 0)
	pos = int(util.AlignInt32(int32(pos), 4))
	pos += m.ImageBufferLength.Wrap(buf, pos)
	m.SetSize(pos - offset)
	return m
}

//export imageReceived
//go:nocheckptr go:nosplit
func imageReceived(image unsafe.Pointer, ctx unsafe.Pointer) {
	cam := (cgo.Handle)(ctx).Value().(FLICamera)

	start := time.Now()

	// Get image dimensions for buffer size
	// width, height, bytes := cam.sdk.GetImageSize()
	// crop, _ := cam.sdk.GetCroppingState()

	// Set
	cam.header.TimestampNs.Set(start.UnixNano())
	// cam.header.SizeX.Set(int32(width))
	// cam.header.SizeY.Set(int32(height))
	// cam.header.OffsetX.Set(int32(crop.Col1))
	// cam.header.OffsetY.Set(int32(crop.Row1))
	// cam.header.ImageBufferLength.Set(int32(bytes))

	cam.imageBuffer.Wrap(image, cam.header.ImageBufferLength.Get())

	const timeout = 100 * time.Microsecond

	for time.Since(start) < timeout {
		ret := cam.publication.Offer2(cam.headerBuffer, 0,
			int32(cam.header.Size()), cam.imageBuffer, 0,
			cam.imageBuffer.Capacity(), nil)
		switch ret {
		// Retry on AdminAction and BackPressured
		case aeron.AdminAction, aeron.BackPressured:
			continue
		// Otherwise return as completed
		default:
			return
		}
	}
}
