package rtsp

import (
	"bufio"
	"fmt"
	"github.com/teris-io/shortid"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	. "github.com/wgsP/engine/v3"
	. "github.com/wgsP/utils/v3"
)

var config = struct {
	ListenAddr   string
	Timeout      int
	Reconnect    bool
	AutoPullList map[string]string
}{":554", 0, false, nil}

func init() {
	InstallPlugin(&PluginConfig{
		Name:   "RTSP",
		Config: &config,
		Run:    runPlugin,
	})
}
func runPlugin() {
	http.HandleFunc("/api/rtsp/list", func(w http.ResponseWriter, r *http.Request) {
		sse := NewSSE(w, r.Context())
		var err error
		for tick := time.NewTicker(time.Second); err == nil; <-tick.C {
			var info []*RTSP
			for _, s := range Streams.ToList() {
				if rtsp, ok := s.ExtraProp.(*RTSP); ok {
					info = append(info, rtsp)
				}
			}
			err = sse.WriteJSON(info)
		}
	})
	http.HandleFunc("/api/rtsp/pull", func(w http.ResponseWriter, r *http.Request) {
		CORS(w, r)
		targetURL := r.URL.Query().Get("target")
		streamPath := r.URL.Query().Get("streamPath")
		if err := (&RTSP{RTSPClientInfo: RTSPClientInfo{Agent: "Monibuca"}}).PullStream(streamPath, targetURL); err == nil {
			w.Write([]byte(`{"code":0}`))
		} else {
			w.Write([]byte(fmt.Sprintf(`{"code":1,"msg":"%s"}`, err.Error())))
		}
	})
	if len(config.AutoPullList) > 0 {
		for streamPath, url := range config.AutoPullList {
			if err := (&RTSP{RTSPClientInfo: RTSPClientInfo{Agent: "Monibuca"}}).PullStream(streamPath, url); err != nil {
				Println(err)
			}
		}
	}
	if config.ListenAddr != "" {
		go log.Fatal(ListenRtsp(config.ListenAddr))
	}
}

func ListenRtsp(addr string) error {
	defer log.Println("rtsp server start!")
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	var tempDelay time.Duration
	networkBuffer := 204800
	timeoutMillis := config.Timeout
	for {
		conn, err := listener.Accept()
		conn.(*net.TCPConn).SetNoDelay(false)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				fmt.Printf("rtsp: Accept error: %v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}

		tempDelay = 0
		timeoutTCPConn := &RichConn{conn, time.Duration(timeoutMillis) * time.Millisecond}
		go (&RTSP{
			ID:                 shortid.MustGenerate(),
			Conn:               timeoutTCPConn,
			connRW:             bufio.NewReadWriter(bufio.NewReaderSize(timeoutTCPConn, networkBuffer), bufio.NewWriterSize(timeoutTCPConn, networkBuffer)),
			Timeout:            config.Timeout,
			vRTPChannel:        -1,
			vRTPControlChannel: -1,
			aRTPChannel:        -1,
			aRTPControlChannel: -1,
		}).AcceptPush()
	}
	return nil
}

type RTSP struct {
	*Stream  `json:"-"`
	URL      string
	SDPRaw   string
	InBytes  int
	OutBytes int
	RTSPClientInfo
	ID        string
	Conn      *RichConn `json:"-"`
	connRW    *bufio.ReadWriter
	connWLock sync.RWMutex
	Type      SessionType
	TransType TransType

	SDPMap  map[string]*SDPInfo
	nonce   string
	ASdp    *SDPInfo
	VSdp    *SDPInfo
	Timeout int
	//tcp channels
	aRTPChannel        int
	aRTPControlChannel int
	vRTPChannel        int
	vRTPControlChannel int
	UDPServer          *UDPServer          `json:"-"`
	UDPClient          *UDPClient          `json:"-"`
	Auth               func(string) string `json:"-"`
	HasVideo           bool
	HasAudio           bool
	RtpAudio           *RTPAudio
	RtpVideo           *RTPVideo
}

func (rtsp *RTSP) setVideoTrack() {
	if rtsp.VSdp.Codec == "H264" {
		rtsp.RtpVideo = rtsp.NewRTPVideo(7)
		if len(rtsp.VSdp.SpropParameterSets) > 1 {
			rtsp.RtpVideo.PushNalu(0, 0, rtsp.VSdp.SpropParameterSets...)
		}
	} else if rtsp.VSdp.Codec == "H265" {
		rtsp.RtpVideo = rtsp.NewRTPVideo(12)
		if len(rtsp.VSdp.VPS) > 0 {
			rtsp.RtpVideo.PushNalu(0, 0, rtsp.VSdp.VPS, rtsp.VSdp.SPS, rtsp.VSdp.PPS)
		}
	}
}
func (rtsp *RTSP) setAudioTrack() {
	var at *RTPAudio
	if len(rtsp.ASdp.Config) > 0 {
		at = rtsp.NewRTPAudio(0)
		at.SetASC(rtsp.ASdp.Config)
	} else {
		switch rtsp.ASdp.Codec {
		case "AAC":
			at = rtsp.NewRTPAudio(10)
		case "PCMA":
			at = rtsp.NewRTPAudio(7)
			at.SoundRate = rtsp.ASdp.TimeScale
			at.SoundSize = 16
			at.Channels = 1
			at.ExtraData = []byte{(at.CodecID << 4) | (1 << 1)}
		case "PCMU":
			at = rtsp.NewRTPAudio(8)
			at.SoundRate = rtsp.ASdp.TimeScale
			at.SoundSize = 16
			at.Channels = 1
			at.ExtraData = []byte{(at.CodecID << 4) | (1 << 1)}
		default:
			Printf("rtsp audio codec not support:%s", rtsp.ASdp.Codec)
			return
		}
	}
	rtsp.RtpAudio = at
}

type RTSPClientInfo struct {
	Agent    string
	Session  string
	authLine string
	Seq      int
}
type RichConn struct {
	net.Conn
	timeout time.Duration
}

func (conn *RichConn) Read(b []byte) (n int, err error) {
	if conn.timeout > 0 {
		conn.Conn.SetReadDeadline(time.Now().Add(conn.timeout))
	} else {
		var t time.Time
		conn.Conn.SetReadDeadline(t)
	}
	return conn.Conn.Read(b)
}

func (conn *RichConn) Write(b []byte) (n int, err error) {
	if conn.timeout > 0 {
		conn.Conn.SetWriteDeadline(time.Now().Add(conn.timeout))
	} else {
		var t time.Time
		conn.Conn.SetWriteDeadline(t)
	}
	return conn.Conn.Write(b)
}
