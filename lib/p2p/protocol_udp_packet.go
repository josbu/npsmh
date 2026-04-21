package p2p

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/djylb/nps/lib/common"
)

func newUDPPacket(sessionID, token, role, typ string) *UDPPacket {
	return newUDPPacketWithWire(sessionID, token, role, typ, P2PWireSpec{RouteID: sessionID})
}

func newUDPPacketWithWire(sessionID, token, role, typ string, wire P2PWireSpec) *UDPPacket {
	wire = NormalizeP2PWireSpec(wire, sessionID)
	return &UDPPacket{
		WireID:          wire.RouteID,
		PayloadMinBytes: wire.PayloadMinBytes,
		PayloadMaxBytes: wire.PayloadMaxBytes,
		Type:            typ,
		SessionID:       sessionID,
		Token:           token,
		Role:            role,
		Timestamp:       time.Now().UnixMilli(),
	}
}

func EncodeUDPPacket(pkt *UDPPacket) ([]byte, error) {
	if pkt == nil {
		return nil, fmt.Errorf("nil udp packet")
	}
	if pkt.WireID == "" {
		pkt.WireID = pkt.SessionID
	}
	if pkt.Timestamp == 0 {
		pkt.Timestamp = time.Now().UnixMilli()
	}
	nonce, nonceText, err := udpPacketNonceBytes(pkt.Nonce)
	if err != nil {
		return nil, err
	}
	pkt.Nonce = nonceText
	route, err := udpPacketRouteBytes(pkt.WireID)
	if err != nil {
		return nil, err
	}
	payload, err := encodeUDPPacketPayload(pkt)
	if err != nil {
		return nil, err
	}
	sealed, err := sealUDPPayload(pkt.Token, hex.EncodeToString(route[:]), nonce, payload)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, udpPacketEnvelopeSize+len(sealed))
	copy(raw[:udpPacketRouteSize], route[:])
	copy(raw[udpPacketRouteSize:udpPacketEnvelopeSize], nonce)
	copy(raw[udpPacketEnvelopeSize:], sealed)
	return raw, nil
}

func DecodeUDPPacket(data []byte, token string) (*UDPPacket, error) {
	wire, err := parseUDPPacketWire(data)
	if err != nil {
		return nil, err
	}
	return decodeUDPPacketWire(wire, UDPPacketLookupResult{Token: token})
}

func DecodeUDPPacketWithLookup(data []byte, lookup func(routeKey string) (UDPPacketLookupResult, bool)) (*UDPPacket, error) {
	wire, err := parseUDPPacketWire(data)
	if err != nil {
		return nil, err
	}
	if lookup == nil {
		return nil, ErrP2PTokenMismatch
	}
	route, ok := lookup(wire.RouteKey)
	if !ok || route.Token == "" {
		return nil, ErrP2PTokenMismatch
	}
	return decodeUDPPacketWire(wire, route)
}

func NewProbeAckPacket(sessionID, token, role string, probePort int, observedAddr string, extraReply bool) *UDPPacket {
	return NewProbeAckPacketWithWire(sessionID, token, role, probePort, observedAddr, extraReply, P2PWireSpec{RouteID: sessionID})
}

func NewProbeAckPacketWithWire(sessionID, token, role string, probePort int, observedAddr string, extraReply bool, wire P2PWireSpec) *UDPPacket {
	packetType := packetTypeAck
	if extraReply {
		packetType = packetTypeProbeX
	}
	packet := newUDPPacketWithWire(sessionID, token, role, packetType, wire)
	packet.ProbePort = probePort
	packet.ObservedAddr = observedAddr
	packet.ExtraReply = extraReply
	return packet
}

func decodeUDPPacketWire(wire udpWirePacket, lookup UDPPacketLookupResult) (*UDPPacket, error) {
	if lookup.Token == "" {
		return nil, ErrP2PTokenMismatch
	}
	plaintext, err := openUDPPayload(lookup.Token, wire.RouteKey, wire.Nonce, wire.Ciphertext)
	if err != nil {
		return nil, ErrP2PTokenMismatch
	}
	packet, err := decodeUDPPacketPayload(plaintext)
	if err != nil {
		return nil, ErrP2PTokenMismatch
	}
	if lookup.SessionID != "" && lookup.SessionID != packet.SessionID {
		return nil, ErrP2PTokenMismatch
	}
	packet.Token = lookup.Token
	packet.WireID = wire.RouteKey
	packet.Nonce = hex.EncodeToString(wire.Nonce)
	return packet, nil
}

func encodeUDPPacketPayload(pkt *UDPPacket) ([]byte, error) {
	if pkt == nil {
		return nil, fmt.Errorf("nil udp packet")
	}
	typeCode, err := udpPacketTypeCode(pkt.Type)
	if err != nil {
		return nil, err
	}
	roleCode, err := udpPacketRoleCode(pkt.Role)
	if err != nil {
		return nil, err
	}
	sessionID := []byte(pkt.SessionID)
	observedAddr := []byte(pkt.ObservedAddr)
	if len(sessionID) == 0 || len(sessionID) > 255 || len(observedAddr) > 255 {
		return nil, fmt.Errorf("invalid udp payload lengths")
	}
	baseLen := 20 + len(sessionID) + len(observedAddr) + 2
	targetLen, err := randomPayloadTarget(baseLen, pkt.PayloadMinBytes, pkt.PayloadMaxBytes)
	if err != nil {
		return nil, err
	}
	padLen := targetLen - baseLen
	if padLen < 0 {
		padLen = 0
	}
	payload := make([]byte, baseLen+padLen)
	payload[0] = udpPayloadVersion
	payload[1] = typeCode
	payload[2] = roleCode
	if pkt.ExtraReply {
		payload[3] = udpFlagExtraReply
	}
	binary.BigEndian.PutUint16(payload[4:6], uint16(pkt.ProbePort))
	binary.BigEndian.PutUint64(payload[6:14], uint64(pkt.Timestamp))
	binary.BigEndian.PutUint32(payload[14:18], pkt.NominationEpoch)
	payload[18] = byte(len(sessionID))
	payload[19] = byte(len(observedAddr))
	offset := 20
	copy(payload[offset:offset+len(sessionID)], sessionID)
	offset += len(sessionID)
	copy(payload[offset:offset+len(observedAddr)], observedAddr)
	offset += len(observedAddr)
	binary.BigEndian.PutUint16(payload[offset:offset+2], uint16(padLen))
	offset += 2
	if padLen > 0 {
		if _, err := rand.Read(payload[offset : offset+padLen]); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func decodeUDPPacketPayload(payload []byte) (*UDPPacket, error) {
	if len(payload) < 22 || payload[0] != udpPayloadVersion {
		return nil, ErrP2PTokenMismatch
	}
	packetType, err := udpPacketTypeFromCode(payload[1])
	if err != nil {
		return nil, err
	}
	role, err := udpPacketRoleFromCode(payload[2])
	if err != nil {
		return nil, err
	}
	sessionLen := int(payload[18])
	observedLen := int(payload[19])
	offset := 20
	if len(payload) < offset+sessionLen+observedLen+2 {
		return nil, ErrP2PTokenMismatch
	}
	sessionID := string(payload[offset : offset+sessionLen])
	offset += sessionLen
	observedAddr := string(payload[offset : offset+observedLen])
	offset += observedLen
	padLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	offset += 2
	if len(payload) != offset+padLen || sessionID == "" {
		return nil, ErrP2PTokenMismatch
	}
	return &UDPPacket{
		SessionID:       sessionID,
		Type:            packetType,
		Role:            role,
		NominationEpoch: binary.BigEndian.Uint32(payload[14:18]),
		ProbePort:       int(binary.BigEndian.Uint16(payload[4:6])),
		ObservedAddr:    observedAddr,
		ExtraReply:      payload[3]&udpFlagExtraReply != 0,
		Timestamp:       int64(binary.BigEndian.Uint64(payload[6:14])),
	}, nil
}

func sealUDPPayload(token, routeKey string, nonce, payload []byte) ([]byte, error) {
	aead, aad, err := udpPacketCipher(token, routeKey)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, payload, aad), nil
}

func openUDPPayload(token, routeKey string, nonce, sealed []byte) ([]byte, error) {
	aead, aad, err := udpPacketCipher(token, routeKey)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, sealed, aad)
}

func udpPacketCipher(token, routeKey string) (cipher.AEAD, []byte, error) {
	if token == "" || routeKey == "" {
		return nil, nil, fmt.Errorf("empty udp packet key material")
	}
	key := sha256.Sum256([]byte("nps-p2p-aead|" + routeKey + "|" + token))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	routeBytes, err := hex.DecodeString(routeKey)
	if err != nil {
		return nil, nil, err
	}
	return aead, routeBytes, nil
}

func udpPacketTypeCode(packetType string) (byte, error) {
	switch packetType {
	case packetTypeProbe:
		return 1, nil
	case packetTypeAck:
		return 2, nil
	case packetTypeProbeX:
		return 3, nil
	case packetTypePunch:
		return 4, nil
	case packetTypeSucc:
		return 5, nil
	case packetTypeEnd:
		return 6, nil
	case packetTypeAccept:
		return 7, nil
	case packetTypeReady:
		return 8, nil
	default:
		return 0, fmt.Errorf("unsupported udp packet type %q", packetType)
	}
}

func udpPacketTypeFromCode(code byte) (string, error) {
	switch code {
	case 1:
		return packetTypeProbe, nil
	case 2:
		return packetTypeAck, nil
	case 3:
		return packetTypeProbeX, nil
	case 4:
		return packetTypePunch, nil
	case 5:
		return packetTypeSucc, nil
	case 6:
		return packetTypeEnd, nil
	case 7:
		return packetTypeAccept, nil
	case 8:
		return packetTypeReady, nil
	default:
		return "", fmt.Errorf("unsupported udp packet type code %d", code)
	}
}

func udpPacketRoleCode(role string) (byte, error) {
	switch role {
	case "":
		return 0, nil
	case common.WORK_P2P_VISITOR:
		return 1, nil
	case common.WORK_P2P_PROVIDER:
		return 2, nil
	default:
		return 0, fmt.Errorf("unsupported udp packet role %q", role)
	}
}

func udpPacketRoleFromCode(code byte) (string, error) {
	switch code {
	case 0:
		return "", nil
	case 1:
		return common.WORK_P2P_VISITOR, nil
	case 2:
		return common.WORK_P2P_PROVIDER, nil
	default:
		return "", fmt.Errorf("unsupported udp packet role code %d", code)
	}
}
