#include <flutter/dart_project.h>
#include <flutter/flutter_view_controller.h>
#include <windows.h>

#include "flutter_window.h"
#include "utils.h"

namespace {

constexpr wchar_t kSingleInstanceMutexName[] =
    L"Local\\informationHub_SingleInstance_Mutex_v1";

// Locate existing informationHub top-level window (even if hidden in System Tray)
// and restore + bring it to the foreground.
void FocusExistingInstance() {
  HWND existing =
      ::FindWindowW(L"FLUTTER_RUNNER_WIN32_WINDOW", L"informationHub");
  if (!existing) {
    existing =
        ::FindWindowW(L"FLUTTER_RUNNER_WIN32_WINDOW", L"intel_hub_client");
  }
  if (existing) {
    ::ShowWindow(existing, SW_SHOW);
    ::ShowWindow(existing, SW_RESTORE);
    ::PostMessageW(existing, WM_SYSCOMMAND, SC_RESTORE, 0);
    ::SetForegroundWindow(existing);
  }
}

}  // namespace

int APIENTRY wWinMain(_In_ HINSTANCE instance, _In_opt_ HINSTANCE prev,
                      _In_ wchar_t *command_line, _In_ int show_command) {
  // Enforce strict Windows single-instance mode: wake existing instance instead of spawning duplicates.
  HANDLE single_instance_mutex =
      ::CreateMutexW(nullptr, TRUE, kSingleInstanceMutexName);
  if (single_instance_mutex && ::GetLastError() == ERROR_ALREADY_EXISTS) {
    FocusExistingInstance();
    ::CloseHandle(single_instance_mutex);
    return EXIT_SUCCESS;
  }

  // Attach to console when present (e.g., 'flutter run') or create a
  // new console when running with a debugger.
  if (!::AttachConsole(ATTACH_PARENT_PROCESS) && ::IsDebuggerPresent()) {
    CreateAndAttachConsole();
  }

  // Initialize COM, so that it is available for use in the library and/or
  // plugins.
  ::CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);

  flutter::DartProject project(L"data");

  std::vector<std::string> command_line_arguments =
      GetCommandLineArguments();

  project.set_dart_entrypoint_arguments(std::move(command_line_arguments));

  FlutterWindow window(project);
  Win32Window::Point origin(10, 10);
  Win32Window::Size size(1280, 720);
  if (!window.Create(L"informationHub", origin, size)) {
    if (single_instance_mutex) {
      ::ReleaseMutex(single_instance_mutex);
      ::CloseHandle(single_instance_mutex);
    }
    return EXIT_FAILURE;
  }
  window.SetQuitOnClose(true);

  ::MSG msg;
  while (::GetMessage(&msg, nullptr, 0, 0)) {
    ::TranslateMessage(&msg);
    ::DispatchMessage(&msg);
  }

  ::CoUninitialize();
  if (single_instance_mutex) {
    ::ReleaseMutex(single_instance_mutex);
    ::CloseHandle(single_instance_mutex);
  }
  return EXIT_SUCCESS;
}
