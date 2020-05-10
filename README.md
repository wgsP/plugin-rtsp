# Monibuca 的RTSP 插件

主要功能是提供RTSP的端口监听接受RTSP推流，以及对RTSP地址进行拉流转发

## 插件名称

RTSP

## 配置
```toml
[RTSP]
ListenAddr  = ":554"
BufferLength  = 2048
AutoPull     = false
RemoteAddr   = "rtsp://localhost/${streamPath}"
```
- ListenAddr 是监听端口，可以将rtsp流推到Monibuca中
- BufferLength是指解析拉取的rtp包的缓冲大小
- AutoPull是指当有用户订阅一个新流的时候自动向远程拉流转发
- RemoteAddr 指远程拉流地址，其中${streamPath}是占位符，实际使用流路径替换。


## 使用方法(拉流转发)
```go
new(RTSP).PullStream("live/user1","rtsp://xxx.xxx.xxx.xxx/live/user1") 
```