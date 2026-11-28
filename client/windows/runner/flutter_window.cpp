#include "flutter_window.h"

#include <dpapi.h>
#include <wincrypt.h>

#include <optional>
#include <string>
#include <vector>

#include <flutter/method_channel.h>
#include <flutter/standard_method_codec.h>

#include "flutter/generated_plugin_registrant.h"

namespace {

bool Base64Encode(const BYTE* data, DWORD len, std::string* out) {
  DWORD b64_len = 0;
  if (!::CryptBinaryToStringA(data, len, CRYPT_STRING_BASE64 | CRYPT_STRING_NOCRLF,
                              nullptr, &b64_len)) {
    return false;
  }
  std::string buf(b64_len, '\0');
  if (!::CryptBinaryToStringA(data, len, CRYPT_STRING_BASE64 | CRYPT_STRING_NOCRLF,
                              buf.data(), &b64_len)) {
    return false;
  }
  buf.resize(b64_len);
  *out = std::move(buf);
  return true;
}

bool Base64Decode(const std::string& in, std::vector<BYTE>* out) {
  DWORD bin_len = 0;
  if (!::CryptStringToBinaryA(in.c_str(), static_cast<DWORD>(in.size()),
                              CRYPT_STRING_BASE64, nullptr, &bin_len, nullptr,
                              nullptr)) {
    return false;
  }
  std::vector<BYTE> buf(bin_len);
  if (!::CryptStringToBinaryA(in.c_str(), static_cast<DWORD>(in.size()),
                              CRYPT_STRING_BASE64, buf.data(), &bin_len, nullptr,
                              nullptr)) {
    return false;
  }
  buf.resize(bin_len);
  *out = std::move(buf);
  return true;
}

std::optional<std::string> DpapiEncrypt(const std::string& plain) {
  DATA_BLOB in_blob{};
  in_blob.pbData =
      reinterpret_cast<BYTE*>(const_cast<char*>(plain.data()));
  in_blob.cbData = static_cast<DWORD>(plain.size());

  DATA_BLOB out_blob{};
  if (!::CryptProtectData(&in_blob, L"informationHub", nullptr, nullptr, nullptr,
                          CRYPTPROTECT_UI_FORBIDDEN, &out_blob)) {
    return std::nullopt;
  }
  std::string encoded;
  const bool ok = Base64Encode(out_blob.pbData, out_blob.cbData, &encoded);
  ::LocalFree(out_blob.pbData);
  if (!ok) return std::nullopt;
  return encoded;
}

std::optional<std::string> DpapiDecrypt(const std::string& cipher_b64) {
  std::vector<BYTE> raw;
  if (!Base64Decode(cipher_b64, &raw) || raw.empty()) {
    return std::nullopt;
  }
  DATA_BLOB in_blob{};
  in_blob.pbData = raw.data();
  in_blob.cbData = static_cast<DWORD>(raw.size());

  DATA_BLOB out_blob{};
  if (!::CryptUnprotectData(&in_blob, nullptr, nullptr, nullptr, nullptr,
                            CRYPTPROTECT_UI_FORBIDDEN, &out_blob)) {
    return std::nullopt;
  }
  std::string plain(reinterpret_cast<const char*>(out_blob.pbData),
                    out_blob.cbData);
  ::LocalFree(out_blob.pbData);
  return plain;
}

}  // namespace

FlutterWindow::FlutterWindow(const flutter::DartProject& project)
    : project_(project) {}

FlutterWindow::~FlutterWindow() {}

bool FlutterWindow::OnCreate() {
  if (!Win32Window::OnCreate()) {
    return false;
  }

  RECT frame = GetClientArea();

  // The size here must match the window dimensions to avoid unnecessary surface
  // creation / destruction in the startup path.
  flutter_controller_ = std::make_unique<flutter::FlutterViewController>(
      frame.right - frame.left, frame.bottom - frame.top, project_);
  // Ensure that basic setup of the controller was successful.
  if (!flutter_controller_->engine() || !flutter_controller_->view()) {
    return false;
  }
  RegisterPlugins(flutter_controller_->engine());

  // Native Win32 DPAPI secure credential channel for encrypted session storage.
  auto secure_channel =
      std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
          flutter_controller_->engine()->messenger(),
          "io.intelhub.client/secure",
          &flutter::StandardMethodCodec::GetInstance());
  secure_channel->SetMethodCallHandler(
      [](const flutter::MethodCall<flutter::EncodableValue>& call,
         std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> result) {
        const auto* args =
            std::get_if<flutter::EncodableMap>(call.arguments());
        if (call.method_name() == "encrypt" && args) {
          auto it = args->find(flutter::EncodableValue("plain"));
          if (it != args->end()) {
            if (const auto* s = std::get_if<std::string>(&it->second)) {
              auto enc = DpapiEncrypt(*s);
              if (enc) {
                result->Success(flutter::EncodableValue(*enc));
                return;
              }
            }
          }
          result->Error("DPAPI_ENCRYPT_FAILED", "CryptProtectData failed");
        } else if (call.method_name() == "decrypt" && args) {
          auto it = args->find(flutter::EncodableValue("cipher"));
          if (it != args->end()) {
            if (const auto* s = std::get_if<std::string>(&it->second)) {
              auto dec = DpapiDecrypt(*s);
              if (dec) {
                result->Success(flutter::EncodableValue(*dec));
                return;
              }
            }
          }
          result->Error("DPAPI_DECRYPT_FAILED", "CryptUnprotectData failed");
        } else {
          result->NotImplemented();
        }
      });
  secure_channel_ = std::move(secure_channel);

  SetChildContent(flutter_controller_->view()->GetNativeWindow());

  flutter_controller_->engine()->SetNextFrameCallback([&]() {
    this->Show();
  });

  // Flutter can complete the first frame before the "show window" callback is
  // registered. The following call ensures a frame is pending to ensure the
  // window is shown. It is a no-op if the first frame hasn't completed yet.
  flutter_controller_->ForceRedraw();

  return true;
}

void FlutterWindow::OnDestroy() {
  secure_channel_.reset();
  if (flutter_controller_) {
    flutter_controller_ = nullptr;
  }

  Win32Window::OnDestroy();
}

LRESULT
FlutterWindow::MessageHandler(HWND hwnd, UINT const message,
                              WPARAM const wparam,
                              LPARAM const lparam) noexcept {
  // Give Flutter, including plugins, an opportunity to handle window messages.
  if (flutter_controller_) {
    std::optional<LRESULT> result =
        flutter_controller_->HandleTopLevelWindowProc(hwnd, message, wparam,
                                                      lparam);
    if (result) {
      return *result;
    }
  }

  switch (message) {
    case WM_FONTCHANGE:
      flutter_controller_->engine()->ReloadSystemFonts();
      break;
  }

  return Win32Window::MessageHandler(hwnd, message, wparam, lparam);
}
