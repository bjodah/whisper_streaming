using System;
using System.Drawing;
using System.IO;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Text.RegularExpressions;
using System.Threading;
using System.Windows.Forms;
using NAudio.Wave;

namespace StrisperClient {
  public class Form1 : Form {
    // UI Elements
    private TextBox txtAddress;
    private ComboBox cbStartMod1, cbStartMod2, cbStopMod1, cbStopMod2;
    private TextBox txtStartKey, txtStopKey;
    private Button btnApplyHotkeys;
    private Label lblStatus;

    // Audio & Network
    private WaveInEvent waveIn;
    private TcpClient tcpClient;
    private NetworkStream netStream;
    private Thread receiveThread;
    private bool isRecording = false;

    // Global Hotkeys P/Invoke
    [DllImport("user32.dll")]
    public static extern bool RegisterHotKey(IntPtr hWnd, int id, int fsModifiers, uint vk);
    [DllImport("user32.dll")]
    public static extern bool UnregisterHotKey(IntPtr hWnd, int id);

    private const int HOTKEY_ID_START = 1;
    private const int HOTKEY_ID_STOP = 2;
    private const int MOD_ALT = 0x0001;
    private const int MOD_CONTROL = 0x0002;
    private const int MOD_SHIFT = 0x0004;
    private const int WM_HOTKEY = 0x0312;

    public Form1() {
      InitializeUI();
      ApplyHotkeys(); // Register defaults on startup
    }

    private void InitializeUI() {
      this.Text = "Strisper Virtual Keyboard";
      this.Size = new Size(330, 260);
      this.FormBorderStyle = FormBorderStyle.FixedDialog;
      this.MaximizeBox = false;
      this.TopMost = true;

      int y = 15;

      // Server Address
      this.Controls.Add(new Label { Text = "Server:", Location = new Point(10, y + 3), AutoSize = true });
      txtAddress = new TextBox { Text = "localhost:43007", Location = new Point(80, y), Width = 215 };
      this.Controls.Add(txtAddress);

      y += 35;

      // Start Hotkey Config
      this.Controls.Add(new Label { Text = "Start Key:", Location = new Point(10, y + 3), AutoSize = true });
      cbStartMod1 = CreateModCombo(80, y, "Ctrl");
      cbStartMod2 = CreateModCombo(155, y, "Alt");
      txtStartKey = new TextBox { Text = "R", Location = new Point(230, y), Width = 65, CharacterCasing = CharacterCasing.Upper };
      this.Controls.Add(cbStartMod1); this.Controls.Add(cbStartMod2); this.Controls.Add(txtStartKey);

      y += 30;

      // Stop Hotkey Config
      this.Controls.Add(new Label { Text = "Stop Key:", Location = new Point(10, y + 3), AutoSize = true });
      cbStopMod1 = CreateModCombo(80, y, "Ctrl");
      cbStopMod2 = CreateModCombo(155, y, "Alt");
      txtStopKey = new TextBox { Text = "S", Location = new Point(230, y), Width = 65, CharacterCasing = CharacterCasing.Upper };
      this.Controls.Add(cbStopMod1); this.Controls.Add(cbStopMod2); this.Controls.Add(txtStopKey);

      y += 35;

      // Apply Button
      btnApplyHotkeys = new Button { Text = "Update Hotkeys", Location = new Point(80, y), Width = 215 };
      btnApplyHotkeys.Click += (s, e) => ApplyHotkeys();
      this.Controls.Add(btnApplyHotkeys);

      y += 45;

      // Status Label
      lblStatus = new Label { Text = "Status: Ready (Waiting for hotkey...)", Location = new Point(10, y), AutoSize = true, Font = new Font(this.Font, FontStyle.Bold), ForeColor = Color.DarkGreen };
      this.Controls.Add(lblStatus);
    }

    private ComboBox CreateModCombo(int x, int y, string defaultVal) {
      var cb = new ComboBox { Location = new Point(x, y), Width = 70, DropDownStyle = ComboBoxStyle.DropDownList };
      cb.Items.AddRange(new object[] { "None", "Ctrl", "Alt", "Shift" });
      cb.SelectedItem = defaultVal;
      return cb;
    }

    private int GetModValue(string modName) {
      if (modName == "Ctrl") return MOD_CONTROL;
      if (modName == "Alt") return MOD_ALT;
      if (modName == "Shift") return MOD_SHIFT;
      return 0; // None
    }

    private void ApplyHotkeys() {
      UnregisterHotKey(this.Handle, HOTKEY_ID_START);
      UnregisterHotKey(this.Handle, HOTKEY_ID_STOP);

      try {
        int startMods = GetModValue(cbStartMod1.Text) | GetModValue(cbStartMod2.Text);
        Keys startK = (Keys)Enum.Parse(typeof(Keys), txtStartKey.Text, true);
        RegisterHotKey(this.Handle, HOTKEY_ID_START, startMods, (uint)startK);

        int stopMods = GetModValue(cbStopMod1.Text) | GetModValue(cbStopMod2.Text);
        Keys stopK = (Keys)Enum.Parse(typeof(Keys), txtStopKey.Text, true);
        RegisterHotKey(this.Handle, HOTKEY_ID_STOP, stopMods, (uint)stopK);

        MessageBox.Show("Hotkeys updated successfully.", "Success", MessageBoxButtons.OK, MessageBoxIcon.Information);
      }
      catch {
        MessageBox.Show("Invalid key selected. Please use a standard letter (e.g. A, B, R, S).", "Error", MessageBoxButtons.OK, MessageBoxIcon.Error);
      }
    }

    // --- Core Audio & Network Logic ---

    private void StartRecording() {
      if (isRecording) return;

      try {
        string[] parts = txtAddress.Text.Split(':');
        string host = parts[0];
        int port = int.Parse(parts[1]);

        tcpClient = new TcpClient(host, port);
        netStream = tcpClient.GetStream();

        waveIn = new WaveInEvent();
        waveIn.WaveFormat = new WaveFormat(16000, 16, 1);
        waveIn.DataAvailable += WaveIn_DataAvailable;

        isRecording = true;
        waveIn.StartRecording();

        receiveThread = new Thread(ReceiveTranscriptions) { IsBackground = true };
        receiveThread.Start();

        UpdateUIState(true);
      }
      catch (Exception ex) {
        MessageBox.Show("Connection Error: " + ex.Message, "Error");
        StopRecording();
      }
    }

    private void StopRecording() {
      if (!isRecording) return;
      isRecording = false;

      waveIn?.StopRecording();
      waveIn?.Dispose();
      waveIn = null;

      netStream?.Close();
      tcpClient?.Close();

      UpdateUIState(false);
    }

    private void WaveIn_DataAvailable(object sender, WaveInEventArgs e) {
      if (isRecording && netStream != null && netStream.CanWrite) {
        try {
          netStream.Write(e.Buffer, 0, e.BytesRecorded);
        }
        catch {
          Invoke(new Action(StopRecording));
        }
      }
    }

    private void ReceiveTranscriptions() {
      try {
        using (StreamReader reader = new StreamReader(netStream, System.Text.Encoding.UTF8)) {
          string line;
          while (isRecording && (line = reader.ReadLine()) != null) {
            var match = Regex.Match(line, @"^(\d+)\s+(\d+)\s+(.*)$");
            if (match.Success) {
              string text = match.Groups[3].Value.Trim().Replace("  ", " ");
              if (!string.IsNullOrEmpty(text)) {
                // Escape Windows SendKeys special characters
                string safeText = Regex.Replace(text, @"([+^%~{}()\[\]])", "{$1}");

                // Simulates typing the text exactly where the cursor is
                SendKeys.SendWait(safeText + " ");
              }
            }
          }
        }
      }
      catch { /* Ignore thread aborts on disconnect */ }
    }

    // --- Utility ---

    private void UpdateUIState(bool recording) {
      if (InvokeRequired) {
        Invoke(new Action(() => UpdateUIState(recording)));
        return;
      }

      txtAddress.Enabled = !recording;
      btnApplyHotkeys.Enabled = !recording;

      lblStatus.Text = recording ? "Status: 🔴 RECORDING (Speak now...)" : "Status: Ready (Waiting for hotkey...)";
      lblStatus.ForeColor = recording ? Color.Red : Color.DarkGreen;
    }

    protected override void WndProc(ref Message m) {
      // Catch the Global Hotkeys when pressed anywhere in Windows
      if (m.Msg == WM_HOTKEY) {
        int id = m.WParam.ToInt32();
        if (id == HOTKEY_ID_START) StartRecording();
        else if (id == HOTKEY_ID_STOP) StopRecording();
      }
      base.WndProc(ref m);
    }

    protected override void OnFormClosing(FormClosingEventArgs e) {
      StopRecording();
      UnregisterHotKey(this.Handle, HOTKEY_ID_START);
      UnregisterHotKey(this.Handle, HOTKEY_ID_STOP);
      base.OnFormClosing(e);
    }
  }
}