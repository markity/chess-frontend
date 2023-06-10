package main

import (
	"chess-frontend/comm/chess"
	"chess-frontend/comm/packets"
	"chess-frontend/comm/settings"
	"chess-frontend/tools"
	"fmt"
	"net"
	"time"

	interactive "github.com/markity/Interactive-Console"
)

type GameState int

const (
	GameStateNone GameState = iota
	GameStateMatching
	GameStateGaming
)

func main() {
	// 连上服务端
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", settings.ServerListenIP, settings.ServerListenPort))
	if err != nil {
		fmt.Printf("failed to dial to server: %v\n", err)
		return
	}

	// 连接完成后发送start_match指令
	startMatchPacket := packets.PacketClientStartMatch{}
	startMatchPacketBytesWithHeader := tools.DoPackWith4BytesHeader(startMatchPacket.MustMarshalToBytes())
	_, err = conn.Write(startMatchPacketBytesWithHeader)
	if err != nil {
		fmt.Printf("network error: %v\n", err)
		return
	}

	heartbeatLoseCount := 0
	gameState := GameStateNone // 初始状态为None

	// gaming的状态
	var selfSide chess.Side
	var myTrun bool
	var waitingMoveResp bool
	var waitingUpgradeOKResp bool

	var waitingUpgrade bool

	var waitingRemoteUpgradeOK bool

	var waitingAcceptDraw bool

	var waitingGameover bool

	var win *interactive.Win

	errChan := make(chan error, 1)
	readFromConnChan := make(chan interface{})
	heartbeatChan := time.NewTicker(settings.HeartbeatInterval * time.Millisecond)
	cmdChan := make(chan string)

	go func() {
		for {
			packetBytes, err := tools.ReadPacketBytesWith4BytesHeader(conn)
			if err != nil {
				errChan <- err
				return
			}

			readFromConnChan <- packets.ClientParse(packetBytes)
		}
	}()

	style := interactive.GetDefaultSytleAttr()
	style.Foreground = interactive.ColorRed

	for {
		select {
		// 能拿到命令, 那么肯定在游戏中
		case cmd := <-cmdChan:
			pattern := tools.ParseCommand(cmd)
			switch pattern.Type {
			case tools.CommandTypeSurrender:
				sur := packets.PacketClientDoSurrender{}
				surBs := tools.DoPackWith4BytesHeader(sur.MustMarshalToBytes())
				conn.Write(surBs)
				waitingGameover = true
				conn.Close()
				win.Stop()
				fmt.Println("你认输了")
				return
			case tools.CommandTypeEmpty:
				// do nothing
			case tools.CommandTypeUnkonwn:
				win.SendLineBackWithColor(style, "未知的命令")
				win.SetBlockInput(false)
			case tools.CommandTypeSwitch:
				if waitingGameover {
					continue
				}
				if waitingAcceptDraw {
					win.SendLineBackWithColor(style, "正在等待你的响应, 你是否同意和棋?")
					win.SetBlockInput(false)
					continue
				}
				if waitingRemoteUpgradeOK {
					win.SendLineBackWithColor(style, "正在等待对方升级兵")
					win.SetBlockInput(false)
					continue
				}

				if !waitingUpgrade {
					win.SendLineBackWithColor(style, "你现在不能升级兵")
					win.SetBlockInput(false)
					continue
				}

				upgradePacket := packets.PacketClientSendPawnUpgrade{
					ChessPieceType: pattern.Swi,
				}
				upgradePacketBytesWithHeader := tools.DoPackWith4BytesHeader(upgradePacket.MustMarshalToBytes())
				_, err := conn.Write(upgradePacketBytesWithHeader)
				if err != nil {
					win.Stop()
					fmt.Printf("network error: %v\n", err)
					return
				}
				waitingUpgrade = false
				waitingUpgradeOKResp = true
			case tools.CommandTypeAccept, tools.CommandTypeRefuse:
				if waitingGameover {
					continue
				}
				if waitingRemoteUpgradeOK {
					win.SendLineBackWithColor(style, "正在等待对方升级兵")
					win.SetBlockInput(false)
					continue
				}
				if waitingUpgrade {
					win.SendLineBackWithColor(style, "你现在应该升级兵")
					win.SetBlockInput(false)
					continue
				}

				if !waitingAcceptDraw {
					win.SendLineBackWithColor(style, "对方没有和棋请求")
					win.SetBlockInput(false)
					continue
				}

				packWheather := packets.PacketClientWheatherAcceptDraw{}
				packWheather.AcceptDraw = pattern.Type == tools.CommandTypeAccept
				packWheatherBytesWithHeader := tools.DoPackWith4BytesHeader(packWheather.MustMarshalToBytes())
				_, err := conn.Write(packWheatherBytesWithHeader)
				if err != nil {
					win.Stop()
					fmt.Printf("network error: %v\n", err)
				}

				if !packWheather.AcceptDraw {
					win.SendLineBackWithColor(style, "现在是你的回合")
					win.SetBlockInput(false)
					waitingAcceptDraw = false
					myTrun = true
				}
			case tools.CommandTypeMove, tools.CommandTypeMoveAndDraw:
				if waitingGameover {
					continue
				}
				if waitingAcceptDraw {
					win.SendLineBackWithColor(style, "正在等待你的响应, 你是否同意和棋?")
					win.SetBlockInput(false)
					continue
				}
				if waitingRemoteUpgradeOK {
					win.SendLineBackWithColor(style, "正在等待对方升级兵")
					win.SetBlockInput(false)
					continue
				}
				if waitingUpgrade {
					win.SendLineBackWithColor(style, "你现在应该升级兵")
					win.SetBlockInput(false)
					continue
				}

				if !myTrun {
					win.SendLineBackWithColor(style, "不是你的回合")
					win.SetBlockInput(false)
					continue
				}

				if pattern.MoveFromX == pattern.MoveToX && pattern.MoveFromY == pattern.MoveToY {
					win.SendLineBackWithColor(style, "两个坐标不能一样")
					win.SetBlockInput(false)
					continue
				}

				movePacket := packets.PacketClientMove{
					FromX:  pattern.MoveFromX,
					FromY:  pattern.MoveFromY,
					ToX:    pattern.MoveToX,
					ToY:    pattern.MoveToY,
					DoDraw: pattern.Type == tools.CommandTypeMoveAndDraw,
				}
				movePacketBytesWithHeader := tools.DoPackWith4BytesHeader(movePacket.MustMarshalToBytes())
				_, err := conn.Write(movePacketBytesWithHeader)
				if err != nil {
					win.Stop()
					fmt.Printf("network error: %v\n", err)
					return
				}

				waitingMoveResp = true
			}
		case <-heartbeatChan.C:
			heartbeatLoseCount++
			if heartbeatLoseCount >= settings.MaxLoseHeartbeat {
				if win != nil {
					win.Stop()
				}
				fmt.Println("network error: connection lost")
				return
			}

			heartbeatPacket := packets.PacketHeartbeat{}
			heartbeatPacketBytesWithHeader := tools.DoPackWith4BytesHeader(heartbeatPacket.MustMarshalToBytes())
			_, err := conn.Write(heartbeatPacketBytesWithHeader)
			if err != nil {
				win.Stop()
				fmt.Printf("network error: %v\n", err)
				return
			}

		case pkIface := <-readFromConnChan:
			switch packet := pkIface.(type) {
			case *packets.PacketHeartbeat:
				heartbeatLoseCount = 0
			case *packets.PacketServerGameOver:
				if gameState != GameStateGaming {
					if win != nil {
						win.Stop()
					}
					fmt.Printf("network error: %v\n", err)
					return
				}

				conn.Close()
				var msg string = "游戏结束, 将在3s后退出"
				if packet.WinnerSide == chess.SideBoth {
					msg += ", 这把平局"
				}
				if packet.WinnerSide == chess.SideWhite {
					msg += ", 白方胜利"
				}
				if packet.WinnerSide == chess.SideBlack {
					msg += ", 黑方胜利"
				}
				if packet.IsSurrender {
					msg += ", 发起投降"
				}
				if packet.IsDraw {
					msg += ", 发起和棋"
				}
				tools.Draw(win, packet.Table, &msg)
				time.Sleep(time.Second * 3)
				win.Stop()
				return
			case *packets.PacketServerMatchedOK:
				if gameState != GameStateMatching {
					if win != nil {
						win.Stop()
					}
					fmt.Println("protocol error")
					return
				}

				gameState = GameStateGaming
				selfSide = packet.Side
				if selfSide == chess.SideWhite {
					myTrun = true
				} else {
					myTrun = false
				}

				winSettings := interactive.GetDefaultConfig()
				winSettings.BlockInputAfterEnter = true
				win = interactive.Run(winSettings)
				cmdChan = win.GetCmdChan()
				var msg *string
				if myTrun {
					m := "你是白方, 你先手"
					msg = &m
				} else {
					m := "你是黑方, 对方先手"
					msg = &m
				}
				tools.Draw(win, packet.Table, msg)
			case *packets.PacketServerMatching:
				if gameState != GameStateNone {
					if win != nil {
						win.Stop()
					}
					fmt.Println("protocol error")
					return
				}

				fmt.Println("正在匹配...")
				gameState = GameStateMatching
			case *packets.PacketServerMoveResp:
				if gameState != GameStateGaming {
					if win != nil {
						win.Stop()
					}
					fmt.Println("protocol error")
					return
				}
				if !waitingMoveResp {
					fmt.Println("protocol error")
					return
				}

				if packet.MoveRespType == packets.PacketTypeServerMoveRespTypeFailed {
					win.SendLineBackWithColor(style, "无效的移动, 请再次检查")
					myTrun = true
					win.SetBlockInput(false)
					continue
				}
				if packet.MoveRespType == packets.PacketTypeServerMoveRespTypeOK {
					myTrun = false
					msg := "现在是对方的回合"
					if packet.KingThreat {
						msg += ", 正在将军"
					}
					waitingMoveResp = false
					tools.Draw(win, packet.TableOnOK, &msg)
					win.SetBlockInput(false)
					continue
				}
				if packet.MoveRespType == packets.PacketTypeServerMoveRespTypePawnUpgrade {
					myTrun = false
					waitingUpgrade = true
					win.SetBlockInput(false)
					msg := "你可以升级, swi bishop/queen/rook/knight"
					tools.Draw(win, packet.TableOnOK, &msg)
					continue
				}
			case *packets.PacketServerNotifyRemoteMove:
				if gameState != GameStateGaming {
					if win != nil {
						win.Stop()
					}
					fmt.Println("protocol error")
					return
				}

				if packet.RemoteRequestDraw {
					waitingAcceptDraw = true
					myTrun = false
					msg := "对方请求议和, accept接受, refuse拒绝"
					tools.Draw(win, packet.Table, &msg)
					continue
				}

				if packet.RemotePawnUpgrade {
					msg := "请等待对方升级"
					myTrun = false
					waitingRemoteUpgradeOK = true
					tools.Draw(win, packet.Table, &msg)
					continue
				}

				msg := "现在是你的回合"
				if packet.KingThreat {
					msg += ", 将军!"
				}
				myTrun = true
				tools.Draw(win, packet.Table, &msg)
			case *packets.PacketServerRemoteLoseConnection:
				if gameState != GameStateGaming {
					win.Stop()
					fmt.Println("protocol error")
					return
				}

				win.Stop()
				fmt.Println("对端连接断开")
				return
			case *packets.PacketServerRemoteUpgradeOK:
				if !waitingRemoteUpgradeOK {
					win.Stop()
					fmt.Println("protocol error")
					return
				}
				waitingRemoteUpgradeOK = false
				msg := "现在是你的回合"
				myTrun = true
				tools.Draw(win, packet.Table, &msg)
			case *packets.PacketServerUpgradeOK:
				if !waitingUpgradeOKResp {
					win.Stop()
					fmt.Println("protocol error")
					return
				}

				waitingUpgradeOKResp = false
				myTrun = false
				msg := "现在是对方的回合"
				tools.Draw(win, packet.Table, &msg)
				win.SetBlockInput(false)
			default:
				win.Stop()
				fmt.Printf("protocol error: unexpected income bytes\n")
				return
			}
		}
	}
}
