package basic

import (
	pk "github.com/mj41/go-mc26/net/packet"
	"github.com/mj41/go-mc26/protocol/play"
)

func (p *Player) handleCookieRequestPacket(packet pk.Packet) error {
	var req play.CookieRequest
	if err := packet.Scan(&req); err != nil {
		return Error{err}
	}
	cookieContent := p.c.Cookies[string(req.Key)]
	resp := play.CookieResponse{Key: req.Key}
	resp.Payload.Has = cookieContent != nil
	resp.Payload.Val = pk.ByteArray(cookieContent)
	if err := p.c.Conn.WritePacket(pk.Marshal(resp.PacketID(), resp)); err != nil {
		return Error{err}
	}
	return nil
}

func (p *Player) handleStoreCookiePacket(packet pk.Packet) error {
	var store play.StoreCookie
	if err := packet.Scan(&store); err != nil {
		return Error{err}
	}
	p.c.Cookies[string(store.Key)] = []byte(store.Payload)
	return nil
}
