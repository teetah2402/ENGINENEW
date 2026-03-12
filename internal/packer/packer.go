package packer

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Master Key Rahasia (Harus 32 Bytes untuk AES-256)
var MasterKey = []byte("fl0w0rk_0s_s3cr3t_m4st3rk3y_256b")

// [ADDED] Prefix mutlak untuk penanda folder Temp OS
const TempPrefix = "aw-2402-"

// [ADDED] Fungsi Sweeping untuk menyapu bersih folder sampah
func CleanupOldTempFolders() {
	tempDir := os.TempDir()
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		log.Printf("[🧹 Sweeper] Gagal membaca direktori temp: %v\n", err)
		return
	}

	count := 0
	for _, entry := range entries {
		// Hanya hapus folder yang berawalan aw-2402-
		if entry.IsDir() && strings.HasPrefix(entry.Name(), TempPrefix) {
			fullPath := filepath.Join(tempDir, entry.Name())
			os.RemoveAll(fullPath)
			count++
		}
	}

	if count > 0 {
		log.Printf("[🧹 Sweeper] Berhasil menghapus %d folder sisa (%s***) dari sistem!\n", count, TempPrefix)
	}
}

// [MODIFIED] GenerateSecretPath membuat folder acak dengan awalan khusus
func GenerateSecretPath() string {
	b := make([]byte, 16)
	rand.Read(b)

	// Tambahkan prefix agar mudah dicari dan disweep
	randomName := TempPrefix + hex.EncodeToString(b)

	secretPath := filepath.Join(os.TempDir(), randomName)
	os.MkdirAll(secretPath, os.ModePerm)

	return secretPath
}

// EncryptAndPack merubah folder mentah menjadi file .flow / .nflow
func EncryptAndPack(sourceDir, outputFile string) error {
	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil { return err }
		if info.IsDir() { return nil }

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil { return err }

		if strings.HasPrefix(relPath, "libs"+string(os.PathSeparator)) { return nil }

		zipFile, err := zipWriter.Create(relPath)
		if err != nil { return err }

		fsFile, err := os.Open(path)
		if err != nil { return err }
		defer fsFile.Close()

		_, err = io.Copy(zipFile, fsFile)
		return err
	})
	if err != nil { return err }
	zipWriter.Close()

	block, err := aes.NewCipher(MasterKey)
	if err != nil { return err }

	gcm, err := cipher.NewGCM(block)
	if err != nil { return err }

	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil { return err }

	cipherText := gcm.Seal(nonce, nonce, buf.Bytes(), nil)

	return os.WriteFile(outputFile, cipherText, 0644)
}

// DecryptAndUnpack dengan Just-In-Time (JIT) Scrambling / Obfuscation
func DecryptAndUnpack(encFile, destDir string) error {
	cipherText, err := os.ReadFile(encFile)
	if err != nil { return err }

	block, err := aes.NewCipher(MasterKey)
	if err != nil { return err }

	gcm, err := cipher.NewGCM(block)
	if err != nil { return err }

	nonceSize := gcm.NonceSize()
	if len(cipherText) < nonceSize { return fmt.Errorf("file terenkripsi rusak/terlalu pendek") }

	nonce, cipherText := cipherText[:nonceSize], cipherText[nonceSize:]
	plainText, err := gcm.Open(nil, nonce, cipherText, nil)
	if err != nil { return err }

	zipReader, err := zip.NewReader(bytes.NewReader(plainText), int64(len(plainText)))
	if err != nil { return err }

	for _, f := range zipReader.File {
		fpath := filepath.Join(destDir, f.Name)

		if !strings.HasPrefix(fpath, filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil { return err }

		rc, err := f.Open()
		if err != nil { return err }

		// -------------------------------------------------------------------
		// ✨ ALGORITMA JIT SCRAMBLING (OBFUSCATION ON-THE-FLY)
		// -------------------------------------------------------------------
		ext := strings.ToLower(filepath.Ext(f.Name))
		isScrambled := false
		var scrambledContent []byte

		if ext == ".py" || ext == ".js" || ext == ".rb" {
			content, err := io.ReadAll(rc)
			if err == nil {
				isScrambled = true

				// 1. Python: Compress ke Zlib -> Encode Base64 -> Eksekusi via Exec
				if ext == ".py" {
					var b bytes.Buffer
					w := zlib.NewWriter(&b)
					w.Write(content)
					w.Close()

					b64 := base64.StdEncoding.EncodeToString(b.Bytes())
					scrambledContent = []byte(fmt.Sprintf("import zlib, base64\nexec(zlib.decompress(base64.b64decode(b'%s')))", b64))
				} else

				// 2. Node.js: Encode Base64 -> Decode & Eval via Buffer (Native Node)
				if ext == ".js" {
					b64 := base64.StdEncoding.EncodeToString(content)
					scrambledContent = []byte(fmt.Sprintf("eval(Buffer.from('%s', 'base64').toString('utf8'));", b64))
				} else

				// 3. Ruby: Compress ke Zlib -> Encode Base64 -> Eksekusi via Eval
				if ext == ".rb" {
					var b bytes.Buffer
					w := zlib.NewWriter(&b)
					w.Write(content)
					w.Close()

					b64 := base64.StdEncoding.EncodeToString(b.Bytes())
					scrambledContent = []byte(fmt.Sprintf("require 'zlib'\nrequire 'base64'\neval(Zlib::Inflate.inflate(Base64.decode64('%s')))", b64))
				}
			}
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		if isScrambled {
			outFile.Write(scrambledContent)
		} else {
			_, err = io.Copy(outFile, rc)
		}

		outFile.Close()
		rc.Close()
		if err != nil { return err }
	}

	return nil
}