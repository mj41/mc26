package component

import (
	"io"

	pk "github.com/mj41/go-mc26/net/packet"
)

var _ DataComponent = (*Profile)(nil)

// PlayerSkinPatch represents the skin patch data.
type PlayerSkinPatch struct {
	Body   pk.Option[pk.String, *pk.String]
	Cape   pk.Option[pk.String, *pk.String]
	Elytra pk.Option[pk.String, *pk.String]
	Model  pk.Option[pk.VarInt, *pk.VarInt]
}

func (p *PlayerSkinPatch) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{&p.Body, &p.Cape, &p.Elytra, &p.Model}.ReadFrom(r)
}

func (p PlayerSkinPatch) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{&p.Body, &p.Cape, &p.Elytra, &p.Model}.WriteTo(w)
}

// Profile component (wire 61).
// Wire: ResolvableProfile = {type:VarInt, switch(type){0:PartialResolvableProfile, 1:GameProfile}, skinPatch:PlayerSkinPatch}
type Profile struct {
	Type pk.VarInt
	// type 0 (partial): name:Option<string>, uuid:Option<UUID>, properties:Array<GameProfileProperty>
	PartialName       pk.Option[pk.String, *pk.String]
	PartialUUID       pk.Option[pk.UUID, *pk.UUID]
	PartialProperties []GameProfileProperty
	// type 1 (complete): uuid:UUID, name:string, properties:Array<GameProfileProperty>
	CompleteUUID       pk.UUID
	CompleteName       pk.String
	CompleteProperties []GameProfileProperty
	// always
	SkinPatch PlayerSkinPatch
}

// ID implements DataComponent.
func (Profile) ID() string {
	return "minecraft:profile"
}

// ReadFrom implements DataComponent.
func (p *Profile) ReadFrom(r io.Reader) (n int64, err error) {
	n, err = p.Type.ReadFrom(r)
	if err != nil {
		return
	}
	var n2 int64
	switch p.Type {
	case 0: // partial
		n2, err = pk.Tuple{
			&p.PartialName,
			&p.PartialUUID,
			pk.Array(&p.PartialProperties),
		}.ReadFrom(r)
	case 1: // complete
		n2, err = pk.Tuple{
			&p.CompleteUUID,
			&p.CompleteName,
			pk.Array(&p.CompleteProperties),
		}.ReadFrom(r)
	}
	n += n2
	if err != nil {
		return
	}
	n2, err = p.SkinPatch.ReadFrom(r)
	n += n2
	return
}

// WriteTo implements DataComponent.
func (p *Profile) WriteTo(w io.Writer) (n int64, err error) {
	n, err = p.Type.WriteTo(w)
	if err != nil {
		return
	}
	var n2 int64
	switch p.Type {
	case 0: // partial
		n2, err = pk.Tuple{
			&p.PartialName,
			&p.PartialUUID,
			pk.Array(&p.PartialProperties),
		}.WriteTo(w)
	case 1: // complete
		n2, err = pk.Tuple{
			&p.CompleteUUID,
			&p.CompleteName,
			pk.Array(&p.CompleteProperties),
		}.WriteTo(w)
	}
	n += n2
	if err != nil {
		return
	}
	n2, err = p.SkinPatch.WriteTo(w)
	n += n2
	return
}
