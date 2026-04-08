package audio

import (
	"encoding/binary"
)

// ToWAV wraps raw 16kHz, 1-channel, 16-bit PCM data in a standard 44-byte WAV header.
func ToWAV(pcm []byte) []byte {
	dataLen := uint32(len(pcm))
	fileLen := dataLen + 36

	header := make([]byte, 44)
	
	// RIFF chunk descriptor
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], fileLen)
	copy(header[8:12], "WAVE")

	// fmt sub-chunk
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16) // Subchunk1Size (16 for PCM)
	binary.LittleEndian.PutUint16(header[20:22], 1)  // AudioFormat (1 for PCM)
	binary.LittleEndian.PutUint16(header[22:24], 1)  // NumChannels (1)
	binary.LittleEndian.PutUint32(header[24:28], 16000) // SampleRate (16000)
	binary.LittleEndian.PutUint32(header[28:32], 32000) // ByteRate (SampleRate * NumChannels * BitsPerSample/8)
	binary.LittleEndian.PutUint16(header[32:34], 2)  // BlockAlign (NumChannels * BitsPerSample/8)
	binary.LittleEndian.PutUint16(header[34:36], 16) // BitsPerSample (16)

	// data sub-chunk
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataLen)

	return append(header, pcm...)
}
