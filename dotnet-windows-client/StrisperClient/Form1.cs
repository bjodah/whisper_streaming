using System;
using System.Collections.Generic;
using System.Drawing;
using System.IO;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using System.Threading;
using System.Windows.Forms;
using NAudio.Wave;

namespace StrisperClient
{
    public class Form1 : Form
    {
        private const int HOTKEY_ID_START = 1;
        private const int HOTKEY_ID_STOP = 2;
        private const int MOD_ALT = 0x0001;
        private const int MOD_CONTROL = 0x0002;
        private const int MOD_SHIFT = 0x0004;
        private const int WM_HOTKEY = 0x0312;
        private static readonly Regex TranscriptLinePattern = new(@"^(\d+)\s+(\d+)\s+(.*)$", RegexOptions.Compiled);
        private static readonly Regex SendKeysEscapePattern = new(@"([+^%~{}()\[\]])", RegexOptions.Compiled);

        private readonly string settingsPath =
            Path.Combine(Application.UserAppDataPath, "settings.json");

        private TextBox txtAddress = null!;
        private ComboBox cbStartMod1 = null!;
        private ComboBox cbStartMod2 = null!;
        private ComboBox cbStopMod1 = null!;
        private ComboBox cbStopMod2 = null!;
        private TextBox txtStartKey = null!;
        private TextBox txtStopKey = null!;
        private Button btnSaveSettings = null!;
        private Label lblStatus = null!;

        private WaveInEvent? waveIn;
        private TcpClient? tcpClient;
        private NetworkStream? netStream;
        private Thread? receiveThread;
        private volatile bool isSessionActive;
        private volatile bool isCapturingAudio;
        private volatile bool isStopping;
        private bool hotkeysInitialized;
        private readonly object pendingTypedTextLock = new();
        private readonly Queue<string> pendingTypedText = new();
        private bool isTypedTextFlushScheduled;
        private HotkeyBinding? activeStartHotkey;
        private HotkeyBinding? activeStopHotkey;

        [DllImport("user32.dll")]
        public static extern bool RegisterHotKey(IntPtr hWnd, int id, int fsModifiers, uint vk);

        [DllImport("user32.dll")]
        public static extern bool UnregisterHotKey(IntPtr hWnd, int id);

        public Form1()
        {
            InitializeUI();
            LoadSettings();
            Load += (_, _) =>
            {
                if (!hotkeysInitialized)
                {
                    ApplyHotkeys(showSuccess: false);
                    hotkeysInitialized = true;
                }
            };
        }

        private void InitializeUI()
        {
            Text = "Strisper Virtual Keyboard";
            ClientSize = new Size(390, 310);
            FormBorderStyle = FormBorderStyle.FixedDialog;
            MaximizeBox = false;
            TopMost = true;

            int y = 15;

            Controls.Add(new Label { Text = "Server:", Location = new Point(10, y + 3), AutoSize = true });
            txtAddress = new TextBox { Text = "localhost:43007", Location = new Point(95, y), Width = 275 };
            Controls.Add(txtAddress);

            y += 32;

            Controls.Add(new Label { Text = "Start Key:", Location = new Point(10, y + 3), AutoSize = true });
            cbStartMod1 = CreateModCombo(95, y, "Ctrl");
            cbStartMod2 = CreateModCombo(175, y, "Alt");
            txtStartKey = new TextBox
            {
                Text = "R",
                Location = new Point(255, y),
                Width = 115,
                CharacterCasing = CharacterCasing.Upper
            };
            Controls.Add(cbStartMod1);
            Controls.Add(cbStartMod2);
            Controls.Add(txtStartKey);

            y += 30;

            Controls.Add(new Label { Text = "Stop Key:", Location = new Point(10, y + 3), AutoSize = true });
            cbStopMod1 = CreateModCombo(95, y, "Ctrl");
            cbStopMod2 = CreateModCombo(175, y, "Alt");
            txtStopKey = new TextBox
            {
                Text = "S",
                Location = new Point(255, y),
                Width = 115,
                CharacterCasing = CharacterCasing.Upper
            };
            Controls.Add(cbStopMod1);
            Controls.Add(cbStopMod2);
            Controls.Add(txtStopKey);

            y += 38;

            btnSaveSettings = new Button { Text = "Save Settings", Location = new Point(95, y), Width = 275 };
            btnSaveSettings.Click += (_, _) => ApplyHotkeys(showSuccess: true);
            Controls.Add(btnSaveSettings);

            y += 46;

            lblStatus = new Label
            {
                Text = "Status: Ready (waiting for hotkey...)",
                Location = new Point(10, y),
                AutoSize = true,
                Font = new Font(Font, FontStyle.Bold),
                ForeColor = Color.DarkGreen
            };
            Controls.Add(lblStatus);
        }

        private ComboBox CreateModCombo(int x, int y, string defaultValue)
        {
            var comboBox = new ComboBox
            {
                Location = new Point(x, y),
                Width = 75,
                DropDownStyle = ComboBoxStyle.DropDownList
            };
            comboBox.Items.AddRange(new object[] { "None", "Ctrl", "Alt", "Shift" });
            comboBox.SelectedItem = defaultValue;
            return comboBox;
        }

        private int GetModValue(string modName) => modName switch
        {
            "Ctrl" => MOD_CONTROL,
            "Alt" => MOD_ALT,
            "Shift" => MOD_SHIFT,
            _ => 0,
        };

        private bool ApplyHotkeys(bool showSuccess)
        {
            if (!TryBuildHotkeyBinding(cbStartMod1, cbStartMod2, txtStartKey, "start", out var startBinding, out var startError))
            {
                MessageBox.Show(this, startError, "Invalid Start Hotkey", MessageBoxButtons.OK, MessageBoxIcon.Error);
                return false;
            }

            if (!TryBuildHotkeyBinding(cbStopMod1, cbStopMod2, txtStopKey, "stop", out var stopBinding, out var stopError))
            {
                MessageBox.Show(this, stopError, "Invalid Stop Hotkey", MessageBoxButtons.OK, MessageBoxIcon.Error);
                return false;
            }

            if (startBinding.Modifiers == stopBinding.Modifiers && startBinding.Key == stopBinding.Key)
            {
                MessageBox.Show(this, "Start and stop hotkeys must be different.", "Invalid Hotkeys", MessageBoxButtons.OK, MessageBoxIcon.Error);
                return false;
            }

            var previousStart = activeStartHotkey;
            var previousStop = activeStopHotkey;

            UnregisterHotkeys();
            activeStartHotkey = null;
            activeStopHotkey = null;

            if (!TryRegisterHotkey(HOTKEY_ID_START, startBinding, out var registrationError))
            {
                RestoreHotkeys(previousStart, previousStop);
                MessageBox.Show(this, registrationError, "Hotkey Registration Failed", MessageBoxButtons.OK, MessageBoxIcon.Error);
                return false;
            }

            if (!TryRegisterHotkey(HOTKEY_ID_STOP, stopBinding, out registrationError))
            {
                UnregisterHotKey(Handle, HOTKEY_ID_START);
                RestoreHotkeys(previousStart, previousStop);
                MessageBox.Show(this, registrationError, "Hotkey Registration Failed", MessageBoxButtons.OK, MessageBoxIcon.Error);
                return false;
            }

            activeStartHotkey = startBinding;
            activeStopHotkey = stopBinding;
            SaveSettings();

            if (showSuccess)
            {
                MessageBox.Show(this, "Settings saved and hotkeys updated.", "Success", MessageBoxButtons.OK, MessageBoxIcon.Information);
            }

            return true;
        }

        private bool TryBuildHotkeyBinding(
            ComboBox firstModifierBox,
            ComboBox secondModifierBox,
            TextBox keyTextBox,
            string hotkeyName,
            out HotkeyBinding binding,
            out string errorMessage)
        {
            string firstModifier = firstModifierBox.Text;
            string secondModifier = secondModifierBox.Text;

            if (firstModifier != "None" && firstModifier == secondModifier)
            {
                binding = default;
                errorMessage = $"The {hotkeyName} hotkey uses the same modifier twice.";
                return false;
            }

            string keyText = keyTextBox.Text.Trim();
            if (string.IsNullOrWhiteSpace(keyText))
            {
                binding = default;
                errorMessage = $"The {hotkeyName} hotkey key cannot be empty.";
                return false;
            }

            if (!Enum.TryParse(keyText, true, out Keys key) || key == Keys.None)
            {
                binding = default;
                errorMessage = $"The {hotkeyName} hotkey key '{keyText}' is not a valid Windows key name.";
                return false;
            }

            binding = new HotkeyBinding(
                GetModValue(firstModifier) | GetModValue(secondModifier),
                key);
            errorMessage = string.Empty;
            return true;
        }

        private bool TryRegisterHotkey(int id, HotkeyBinding binding, out string errorMessage)
        {
            if (RegisterHotKey(Handle, id, binding.Modifiers, (uint)binding.Key))
            {
                errorMessage = string.Empty;
                return true;
            }

            errorMessage = $"Could not register {binding.DisplayName}. It may already be in use by another application.";
            return false;
        }

        private void RestoreHotkeys(HotkeyBinding? startBinding, HotkeyBinding? stopBinding)
        {
            activeStartHotkey = null;
            activeStopHotkey = null;

            if (startBinding.HasValue && stopBinding.HasValue &&
                TryRegisterHotkey(HOTKEY_ID_START, startBinding.Value, out _) &&
                TryRegisterHotkey(HOTKEY_ID_STOP, stopBinding.Value, out _))
            {
                activeStartHotkey = startBinding;
                activeStopHotkey = stopBinding;
                return;
            }

            UnregisterHotkeys();
        }

        private void UnregisterHotkeys()
        {
            if (!IsHandleCreated)
            {
                return;
            }

            UnregisterHotKey(Handle, HOTKEY_ID_START);
            UnregisterHotKey(Handle, HOTKEY_ID_STOP);
        }

        private void StartRecording()
        {
            if (isSessionActive)
            {
                return;
            }

            if (!TryParseEndpoint(txtAddress.Text, out var host, out var port, out var validationError))
            {
                MessageBox.Show(this, validationError, "Invalid Server Address", MessageBoxButtons.OK, MessageBoxIcon.Error);
                return;
            }

            try
            {
                tcpClient = new TcpClient();
                tcpClient.Connect(host, port);
                netStream = tcpClient.GetStream();

                waveIn = new WaveInEvent
                {
                    WaveFormat = new WaveFormat(16000, 16, 1)
                };
                waveIn.DataAvailable += WaveIn_DataAvailable;

                isSessionActive = true;
                isCapturingAudio = true;
                isStopping = false;
                UpdateUIState();

                receiveThread = new Thread(ReceiveTranscriptions)
                {
                    IsBackground = true,
                    Name = "strisper-receive"
                };
                receiveThread.Start();

                waveIn.StartRecording();
            }
            catch (Exception ex)
            {
                AbortSessionNow();
                MessageBox.Show(this, "Connection Error: " + ex.Message, "Error", MessageBoxButtons.OK, MessageBoxIcon.Error);
            }
        }

        private void StopRecording()
        {
            if (!isSessionActive || isStopping)
            {
                return;
            }

            isCapturingAudio = false;
            isStopping = true;

            DisposeWaveIn();

            try
            {
                tcpClient?.Client.Shutdown(SocketShutdown.Send);
            }
            catch (SocketException)
            {
            }
            catch (ObjectDisposedException)
            {
            }

            UpdateUIState();
        }

        private void AbortSessionNow()
        {
            isCapturingAudio = false;
            isStopping = false;
            isSessionActive = false;

            DisposeWaveIn();
            CleanupNetworkResources();
            receiveThread = null;

            if (!IsDisposed)
            {
                UpdateUIState();
            }
        }

        private void DisposeWaveIn()
        {
            if (waveIn is null)
            {
                return;
            }

            try
            {
                waveIn.DataAvailable -= WaveIn_DataAvailable;
                waveIn.StopRecording();
            }
            catch (InvalidOperationException)
            {
            }
            finally
            {
                waveIn.Dispose();
                waveIn = null;
            }
        }

        private void CleanupNetworkResources()
        {
            try
            {
                netStream?.Dispose();
            }
            catch (IOException)
            {
            }
            finally
            {
                netStream = null;
            }

            try
            {
                tcpClient?.Close();
            }
            finally
            {
                tcpClient = null;
            }
        }

        private void WaveIn_DataAvailable(object? sender, WaveInEventArgs e)
        {
            if (!isCapturingAudio || netStream is null || !netStream.CanWrite)
            {
                return;
            }

            try
            {
                netStream.Write(e.Buffer, 0, e.BytesRecorded);
            }
            catch (IOException)
            {
                if (IsHandleCreated && !IsDisposed)
                {
                    BeginInvoke(new Action(AbortSessionNow));
                }
            }
            catch (ObjectDisposedException)
            {
                if (IsHandleCreated && !IsDisposed)
                {
                    BeginInvoke(new Action(AbortSessionNow));
                }
            }
        }

        private void ReceiveTranscriptions()
        {
            try
            {
                if (netStream is null)
                {
                    return;
                }

                using var reader = new StreamReader(netStream, Encoding.UTF8, detectEncodingFromByteOrderMarks: false, bufferSize: 1024, leaveOpen: true);
                string? line;
                while ((line = reader.ReadLine()) is not null)
                {
                    var match = TranscriptLinePattern.Match(line);
                    if (!match.Success)
                    {
                        continue;
                    }

                    string text = Regex.Replace(match.Groups[3].Value.Trim(), @"\s+", " ");
                    if (!string.IsNullOrEmpty(text))
                    {
                        QueueTypedText(text + " ");
                    }
                }
            }
            catch (IOException)
            {
            }
            catch (ObjectDisposedException)
            {
            }
            finally
            {
                FinalizeSessionFromReceiver();
            }
        }

        private void QueueTypedText(string text)
        {
            if (!IsHandleCreated || IsDisposed)
            {
                return;
            }

            lock (pendingTypedTextLock)
            {
                pendingTypedText.Enqueue(text);
                if (isTypedTextFlushScheduled)
                {
                    return;
                }

                isTypedTextFlushScheduled = true;
            }

            try
            {
                BeginInvoke(new Action(FlushQueuedTypedText));
            }
            catch (ObjectDisposedException)
            {
                lock (pendingTypedTextLock)
                {
                    pendingTypedText.Clear();
                    isTypedTextFlushScheduled = false;
                }
            }
            catch (InvalidOperationException)
            {
                lock (pendingTypedTextLock)
                {
                    pendingTypedText.Clear();
                    isTypedTextFlushScheduled = false;
                }
            }
        }

        private void FlushQueuedTypedText()
        {
            while (true)
            {
                string? text;

                lock (pendingTypedTextLock)
                {
                    if (pendingTypedText.Count == 0)
                    {
                        isTypedTextFlushScheduled = false;
                        return;
                    }

                    text = pendingTypedText.Dequeue();
                }

                string safeText = SendKeysEscapePattern.Replace(text, "{$1}");
                try
                {
                    SendKeys.Send(safeText);
                }
                catch
                {
                    // Ignore transient SendKeys failures and keep draining.
                }
            }
        }

        private void FinalizeSessionFromReceiver()
        {
            if (!IsHandleCreated || IsDisposed || Disposing)
            {
                CleanupNetworkResources();
                receiveThread = null;
                return;
            }

            try
            {
                BeginInvoke(new Action(FinalizeSession));
            }
            catch (InvalidOperationException)
            {
                CleanupNetworkResources();
                receiveThread = null;
            }
        }

        private void FinalizeSession()
        {
            isCapturingAudio = false;
            isStopping = false;
            isSessionActive = false;
            CleanupNetworkResources();
            receiveThread = null;
            UpdateUIState();
        }

        private void UpdateUIState()
        {
            if (InvokeRequired)
            {
                Invoke(new Action(UpdateUIState));
                return;
            }

            bool allowEditing = !isSessionActive && !isStopping;

            txtAddress.Enabled = allowEditing;
            cbStartMod1.Enabled = allowEditing;
            cbStartMod2.Enabled = allowEditing;
            cbStopMod1.Enabled = allowEditing;
            cbStopMod2.Enabled = allowEditing;
            txtStartKey.Enabled = allowEditing;
            txtStopKey.Enabled = allowEditing;
            btnSaveSettings.Enabled = allowEditing;

            if (isCapturingAudio)
            {
                lblStatus.Text = "Status: RECORDING (speak now...)";
                lblStatus.ForeColor = Color.Red;
            }
            else if (isStopping || isSessionActive)
            {
                lblStatus.Text = "Status: Finishing current transcription...";
                lblStatus.ForeColor = Color.DarkOrange;
            }
            else
            {
                lblStatus.Text = "Status: Ready (waiting for hotkey...)";
                lblStatus.ForeColor = Color.DarkGreen;
            }
        }

        private void LoadSettings()
        {
            try
            {
                if (!File.Exists(settingsPath))
                {
                    return;
                }

                var savedSettings = JsonSerializer.Deserialize<PersistedSettings>(File.ReadAllText(settingsPath));
                if (savedSettings is null)
                {
                    return;
                }

                txtAddress.Text = string.IsNullOrWhiteSpace(savedSettings.ServerAddress)
                    ? "localhost:43007"
                    : savedSettings.ServerAddress;
                SetComboValue(cbStartMod1, savedSettings.StartModifier1, "Ctrl");
                SetComboValue(cbStartMod2, savedSettings.StartModifier2, "Alt");
                SetComboValue(cbStopMod1, savedSettings.StopModifier1, "Ctrl");
                SetComboValue(cbStopMod2, savedSettings.StopModifier2, "Alt");

                txtStartKey.Text = string.IsNullOrWhiteSpace(savedSettings.StartKey) ? "R" : savedSettings.StartKey;
                txtStopKey.Text = string.IsNullOrWhiteSpace(savedSettings.StopKey) ? "S" : savedSettings.StopKey;
            }
            catch (Exception)
            {
            }
        }

        private void SaveSettings()
        {
            try
            {
                Directory.CreateDirectory(Path.GetDirectoryName(settingsPath)!);

                var settings = new PersistedSettings
                {
                    ServerAddress = txtAddress.Text.Trim(),
                    StartModifier1 = cbStartMod1.Text,
                    StartModifier2 = cbStartMod2.Text,
                    StopModifier1 = cbStopMod1.Text,
                    StopModifier2 = cbStopMod2.Text,
                    StartKey = txtStartKey.Text.Trim(),
                    StopKey = txtStopKey.Text.Trim(),
                };

                File.WriteAllText(settingsPath, JsonSerializer.Serialize(settings, new JsonSerializerOptions
                {
                    WriteIndented = true
                }));
            }
            catch (Exception)
            {
            }
        }

        private static void SetComboValue(ComboBox comboBox, string? value, string fallback)
        {
            comboBox.SelectedItem = comboBox.Items.Contains(value) ? value : fallback;
        }

        private static bool TryParseEndpoint(string rawValue, out string host, out int port, out string errorMessage)
        {
            host = string.Empty;
            port = 0;

            string value = rawValue.Trim();
            if (string.IsNullOrWhiteSpace(value))
            {
                errorMessage = "Enter a server address like localhost:43007 or [::1]:43007.";
                return false;
            }

            string portText;
            if (value.StartsWith("[", StringComparison.Ordinal))
            {
                int closingBracketIndex = value.IndexOf(']');
                if (closingBracketIndex <= 1 || closingBracketIndex + 2 > value.Length || value[closingBracketIndex + 1] != ':')
                {
                    errorMessage = "IPv6 addresses must use the form [address]:port.";
                    return false;
                }

                host = value.Substring(1, closingBracketIndex - 1);
                portText = value[(closingBracketIndex + 2)..];
            }
            else
            {
                int lastColonIndex = value.LastIndexOf(':');
                if (lastColonIndex <= 0 || lastColonIndex == value.Length - 1)
                {
                    errorMessage = "Server address must include both host and port, for example localhost:43007.";
                    return false;
                }

                if (value.IndexOf(':') != lastColonIndex)
                {
                    errorMessage = "IPv6 addresses must be wrapped in brackets, for example [::1]:43007.";
                    return false;
                }

                host = value[..lastColonIndex];
                portText = value[(lastColonIndex + 1)..];
            }

            if (string.IsNullOrWhiteSpace(host))
            {
                errorMessage = "Server host cannot be empty.";
                return false;
            }

            if (!int.TryParse(portText, out port) || port < 1 || port > 65535)
            {
                errorMessage = "Port must be a number between 1 and 65535.";
                return false;
            }

            errorMessage = string.Empty;
            return true;
        }

        protected override void WndProc(ref Message m)
        {
            if (m.Msg == WM_HOTKEY)
            {
                int id = m.WParam.ToInt32();
                if (id == HOTKEY_ID_START)
                {
                    StartRecording();
                }
                else if (id == HOTKEY_ID_STOP)
                {
                    StopRecording();
                }
            }

            base.WndProc(ref m);
        }

        protected override void OnFormClosing(FormClosingEventArgs e)
        {
            SaveSettings();
            AbortSessionNow();
            UnregisterHotkeys();
            base.OnFormClosing(e);
        }

        private readonly struct HotkeyBinding
        {
            public HotkeyBinding(int modifiers, Keys key)
            {
                Modifiers = modifiers;
                Key = key;
            }

            public int Modifiers { get; }
            public Keys Key { get; }

            public string DisplayName
            {
                get
                {
                    var builder = new StringBuilder();
                    if ((Modifiers & MOD_CONTROL) != 0)
                    {
                        builder.Append("Ctrl+");
                    }

                    if ((Modifiers & MOD_ALT) != 0)
                    {
                        builder.Append("Alt+");
                    }

                    if ((Modifiers & MOD_SHIFT) != 0)
                    {
                        builder.Append("Shift+");
                    }

                    builder.Append(Key);
                    return builder.ToString();
                }
            }
        }

        private sealed class PersistedSettings
        {
            public string? ServerAddress { get; set; }
            public string? StartModifier1 { get; set; }
            public string? StartModifier2 { get; set; }
            public string? StopModifier1 { get; set; }
            public string? StopModifier2 { get; set; }
            public string? StartKey { get; set; }
            public string? StopKey { get; set; }
        }
    }
}
