package listener

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
	"github.com/cep21/gohelpers/a"
	"github.com/cep21/gohelpers/workarounds"
	"github.com/signalfuse/signalfxproxy/config"
	"github.com/signalfuse/signalfxproxy/core"
	"github.com/signalfuse/signalfxproxy/core/value"
	"github.com/signalfuse/signalfxproxy/listener/metricdeconstructor"
	"github.com/stretchr/testify/assert"
)

var readerReadBytesObj a.ReaderReadBytesObj

func init() {
	readerReadBytes = readerReadBytesObj.Execute
}

type basicDatapointStreamingAPI struct {
	channel chan core.Datapoint
}

func (api *basicDatapointStreamingAPI) DatapointsChannel() chan<- core.Datapoint {
	return api.channel
}

func (api *basicDatapointStreamingAPI) Name() string {
	return ""
}

func TestCarbonCoverOriginalReaderReadBytes(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader([]byte("test*test")))
	b, err := originalReaderReadBytes(r, '*')
	assert.Nil(t, err)
	assert.Equal(t, "test*", string(b), "Did not get test string back")
}

func TestCarbonInvalidCarbonListenerLoader(t *testing.T) {
	listenFrom := &config.ListenFrom{
		ListenAddr: workarounds.GolangDoesnotAllowPointerToStringLiteral("127.0.0.1:999999"),
	}
	sendTo := &basicDatapointStreamingAPI{}
	_, err := CarbonListenerLoader(sendTo, listenFrom)
	assert.NotEqual(t, nil, err, "Should get an error making")
}

func TestCarbonInvalidCarbonDeconstructorListenerLoader(t *testing.T) {
	listenFrom := &config.ListenFrom{
		ListenAddr:          workarounds.GolangDoesnotAllowPointerToStringLiteral("127.0.0.1:12247"),
		MetricDeconstructor: workarounds.GolangDoesnotAllowPointerToStringLiteral("UNKNOWN"),
	}
	sendTo := &basicDatapointStreamingAPI{}
	_, err := CarbonListenerLoader(sendTo, listenFrom)
	assert.NotEqual(t, nil, err, "Should get an error making")
}

func TestCarbonListenerLoader(t *testing.T) {
	listenFrom := &config.ListenFrom{
		ListenAddr:           workarounds.GolangDoesnotAllowPointerToStringLiteral("127.0.0.1:0"),
		ServerAcceptDeadline: workarounds.GolangDoesnotAllowPointerToTimeLiteral(time.Millisecond),
	}
	sendTo := &basicDatapointStreamingAPI{
		channel: make(chan core.Datapoint),
	}
	listener, err := CarbonListenerLoader(sendTo, listenFrom)
	assert.Equal(t, nil, err, "Should be ok to make")
	defer listener.Close()
	listeningDialAddress := fmt.Sprintf("127.0.0.1:%d", getPortFromListener(listener))
	assert.Equal(t, 4, len(listener.GetStats()), "Should have no stats")
	assert.NotEqual(t, listener, err, "Should be ok to make")

	// Wait for the connection to timeout
	time.Sleep(3 * time.Millisecond)

	conn, err := net.Dial("tcp", listeningDialAddress)
	assert.Equal(t, nil, err, "Should be ok to make")
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s %d %d\n\nINVALIDLINE", "ametric", 2, 2)
	_, err = buf.WriteTo(conn)
	conn.Close()
	assert.Equal(t, nil, err, "Should be ok to write")
	datapoint := <-sendTo.channel
	assert.Equal(t, "ametric", datapoint.Metric(), "Should be metric")
	i := datapoint.Value().(value.IntDatapoint).IntValue()
	assert.Equal(t, int64(2), i, "Should get 2")

	for len(sendTo.channel) > 0 {
		_ = <-sendTo.channel
	}
}

func getPortFromListener(l interface{}) uint16 {
	return (uint16)(l.(NetworkListener).GetAddr().(*net.TCPAddr).Port)
}

func TestCarbonListenerLoader2(t *testing.T) {
	listenFrom := &config.ListenFrom{
		ListenAddr: workarounds.GolangDoesnotAllowPointerToStringLiteral("127.0.0.1:0"),
	}
	sendTo := &basicDatapointStreamingAPI{
		channel: make(chan core.Datapoint),
	}
	listener, err := CarbonListenerLoader(sendTo, listenFrom)
	listeningDialAddress := fmt.Sprintf("127.0.0.1:%d", getPortFromListener(listener))
	assert.Equal(t, nil, err, "Should be ok to make")
	defer listener.Close()
	assert.Equal(t, "tcp", listener.(NetworkListener).GetAddr().Network())
	carbonlistener, _ := listener.(*carbonListener)
	carbonlistener.metricDeconstructor, err = metricdeconstructor.Load("commakeys", "")
	assert.Nil(t, err)
	conn, err := net.Dial("tcp", listeningDialAddress)
	assert.Equal(t, nil, err, "Should be ok to make")
	buf := bytes.Buffer{}
	fmt.Fprintf(&buf, "a.metric.name[host:bob,type:dev] 3 3")
	_, err = buf.WriteTo(conn)
	conn.Close()
	assert.Equal(t, nil, err, "Should be ok to write")
	datapoint := <-sendTo.channel
	assert.Equal(t, "a.metric.name", datapoint.Metric(), "Should be metric")
	assert.Equal(t, map[string]string{"host": "bob", "type": "dev"}, datapoint.Dimensions(), "Did not parse dimensions")
	i := datapoint.Value().(value.IntDatapoint).IntValue()
	assert.Equal(t, int64(3), i, "Should get 3")

	carbonlistener.metricDeconstructor, _ = metricdeconstructor.Load("", "")

	func() {
		readerErrorSignal := make(chan bool)
		readerReadBytesObj.UseFunction(func(reader *bufio.Reader, delim byte) ([]byte, error) {
			readerErrorSignal <- true
			return nil, errors.New("error reading from reader")
		})
		defer readerReadBytesObj.Reset()
		conn, err = net.Dial("tcp", listeningDialAddress)
		assert.Equal(t, nil, err, "Should be ok to make")
		var buf2 bytes.Buffer
		fmt.Fprintf(&buf2, "ametric 2 2\n")
		_, err = buf2.WriteTo(conn)
		conn.Close()
		_ = <-readerErrorSignal
	}()

	time.Sleep(time.Millisecond)

	func() {
		readerErrorSignal := make(chan bool)
		readerReadBytesObj.UseFunction(func(reader *bufio.Reader, delim byte) ([]byte, error) {
			readerErrorSignal <- true
			return []byte("ametric 3 2\n"), io.EOF
		})
		defer readerReadBytesObj.Reset()
		conn, err = net.Dial("tcp", listeningDialAddress)
		assert.Equal(t, nil, err, "Should be ok to make")
		var buf3 bytes.Buffer
		fmt.Fprintf(&buf3, "ametric 3 2\n")
		_, err = buf3.WriteTo(conn)
		conn.Close()
		_ = <-readerErrorSignal
		datapoint = <-sendTo.channel
		i = datapoint.Value().(value.IntDatapoint).IntValue()
		assert.Equal(t, int64(3), i, "Should get 3")
	}()
}

func BenchmarkCarbonListening(b *testing.B) {
	listenFrom := &config.ListenFrom{
		ListenAddr: workarounds.GolangDoesnotAllowPointerToStringLiteral("127.0.0.1:0"),
	}
	bytesToSend := []byte("ametric 123 1234\n")
	sendTo := &basicDatapointStreamingAPI{
		channel: make(chan core.Datapoint, 10000),
	}
	listener, err := CarbonListenerLoader(sendTo, listenFrom)
	if err != nil {
		b.Fatal(err)
	}
	carbonListener := listener.(*carbonListener)
	defer listener.Close()

	conn, err := net.Dial(carbonListener.finalAddr.Network(), carbonListener.finalAddr.String())
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()

	doneReadingPoints := make(chan bool)

	b.ResetTimer()
	go func() {
		for i := 0; i < b.N; i++ {
			dp := <-sendTo.channel
			if dp.Metric() != "ametric" {
				b.Fatalf("Invalid metric %s", dp.Metric())
			}
		}
		doneReadingPoints <- true
	}()

	n := int64(0)
	for i := 0; i < b.N; i++ {
		n += int64(len(bytesToSend))
		_, err = bytes.NewBuffer(bytesToSend).WriteTo(conn)
	}
	_ = <-doneReadingPoints
	b.SetBytes(n)
}
