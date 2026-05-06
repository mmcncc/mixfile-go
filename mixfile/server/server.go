package server

import (
	"fmt"
	"mixfile-go/mixfile"
	"mixfile-go/mixfile/shareinfo"
	"mixfile-go/mixfile/utils"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type MixFileServer struct {
	HttpClient        *http.Client
	DownloadTaskCount int
}

func (s *MixFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 添加首页路由逻辑
	if r.URL.Path == "/" {
		s.handleIndex(w, r)
		return
	}

	// 原有的下载路由
	if r.URL.Path == "/api/download" {
		s.handleDownload(w, r)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *MixFileServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>MixFile 提取工具</title>
    <style>
        body {
            display: flex;
            justify-content: center;
            align-items: center;
            height: 100vh;
            margin: 0;
            background-color: #f0f2f5;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
        }
        .card {
            background: white;
            padding: 40px;
            border-radius: 12px;
            box-shadow: 0 4px 12px rgba(0,0,0,0.1);
            text-align: center;
            width: 100%;
            max-width: 400px;
        }
        h2 { color: #1a1a1a; margin-bottom: 24px; }
        input {
            width: 100%;
            padding: 12px;
            margin-bottom: 20px;
            border: 1px solid #d9d9d9;
            border-radius: 6px;
            box-sizing: border-box;
            font-size: 16px;
        }
        button {
            width: 100%;
            padding: 12px;
            background-color: #1677ff;
            color: white;
            border: none;
            border-radius: 6px;
            font-size: 16px;
            cursor: pointer;
            transition: background 0.3s;
        }
        button:hover { background-color: #4096ff; }
    </style>
</head>
<body>
    <div class="card">
        <h2>MixFile 文件提取</h2>
        <input type="text" id="shareCode" placeholder="请输入 mf:// 开头的分享码" />
        <button onclick="startDownload()">开始下载</button>
    </div>

    <script>
        function startDownload() {
            const code = document.getElementById('shareCode').value.trim();
            if (!code) {
                alert('请输入分享码');
                return;
            }
            // 跳转到下载接口
            window.location.href = '/api/download?s=' + encodeURIComponent(code);
        }
        
        // 支持回车键触发
        document.getElementById('shareCode').addEventListener('keypress', function (e) {
            if (e.key === 'Enter') startDownload();
        });
    </script>
</body>
</html>
`)
}

func (s *MixFileServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	shareInfoData := query.Get("s")
	if shareInfoData == "" {
		http.Error(w, "分享信息为空", http.StatusInternalServerError)
		return
	}

	shareInfoData = utils.SubstringAfter(shareInfoData, "mf://")

	// 1. 解析 ShareInfo (使用之前写的 FromString)
	shareInfo, err := shareinfo.FromString(shareInfoData)
	if err != nil {
		http.Error(w, "解析文件失败", http.StatusInternalServerError)
		return
	}

	// 2. 获取 MixFile 索引
	referer := query.Get("referer")
	if referer == "" {
		referer = shareInfo.Referer
	}

	// 假设 fetchFile 逻辑已实现
	mixFileBytes, err := shareInfo.DoFetchFile(s.HttpClient, shareInfo.URL, referer)
	if err != nil {
		http.Error(w, "解析文件索引失败", http.StatusInternalServerError)
		return
	}
	mixFile, _ := mixfile.FromBytes(mixFileBytes)

	// 3. 处理 Header 和 Range
	name := query.Get("name")
	if name == "" {
		name = shareInfo.FileName
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", url.QueryEscape(name)))
	//w.Header().Set("x-mixfile-code", shareInfo.CachedCode)

	totalFileSize := mixFile.FileSize
	// 1. 先计算好所有的值，但不要急着设置
	var statusCode = http.StatusOK
	contentLength := totalFileSize
	startRange := int64(0)

	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" && strings.HasPrefix(rangeHeader, "bytes=") {
		rangeValue := strings.TrimPrefix(rangeHeader, "bytes=")
		// 即使是 "441843712-"，Split 也会返回 ["441843712", ""]
		parts := strings.Split(rangeValue, "-")

		if len(parts) > 0 && parts[0] != "" {
			start, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
			if err == nil && start >= 0 && start < totalFileSize {
				startRange = start
				statusCode = http.StatusPartialContent

				// 计算实际要发送的长度
				contentLength = totalFileSize - startRange

				// 设置 Content-Range (bytes start-end/total)
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d",
					startRange, totalFileSize-1, totalFileSize))
			}
		}
	}

	// 2. 统一设置 Header (在 WriteHeader 之前)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", url.QueryEscape(name)))
	//w.Header().Set("x-mixfile-code", shareInfo.CachedCode)

	// 3. 最后发送状态码
	w.WriteHeader(statusCode)

	// 4. 开始流式并发下载
	s.writeMixFile(w, shareInfo, mixFile, startRange, referer)
}

func (s *MixFileServer) writeMixFile(w http.ResponseWriter, shareInfo *shareinfo.MixShareInfo, mixFile *mixfile.MixFile, startRange int64, referer string) {
	fileList := mixFile.GetFileListByStartRange(startRange)

	// 计算并发数
	chunkSizeMB := mixFile.ChunkSize / (1024 * 1024)
	if chunkSizeMB < 1 {
		chunkSizeMB = 1
	}
	taskLimit := s.DownloadTaskCount / chunkSizeMB
	if taskLimit < 1 {
		taskLimit = 1
	}

	st := utils.NewSortedTask(taskLimit)
	var wg sync.WaitGroup

	// 错误处理通道
	errChan := make(chan error, 1)

	for i, fileRange := range fileList {
		order := i
		targetURL := fileRange.URL
		offset := fileRange.Offset

		st.Acquire()
		wg.Add(1)

		go func(o int, u string, off int) {
			defer wg.Done()

			// 并发下载数据
			data, err := shareInfo.DoFetchFile(s.HttpClient, u, referer)

			if err != nil {
			    fmt.Println("发生错误: ", err)
				select {
				case errChan <- err:
				default:
				}
				return
			}

			// 按照顺序写入 Response
			err = st.AddAndExecute(o, func() error {
				finalData := data
				if off > 0 && off < len(data) {
					finalData = data[off:]
				}
				_, wErr := w.Write(finalData)
				return wErr
			})

			if err != nil {
				select {
				case errChan <- err:
				default:
				}
			}
		}(order, targetURL, offset)

		// 如果发生错误，停止提交新任务
		if len(errChan) > 0 {
			break
		}
	}

	wg.Wait()
}
