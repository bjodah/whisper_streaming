;;; strisper.el --- Streaming Speech-to-Text using whisper -*- lexical-binding: t; -*-

;; Copyright (C) 2025 Björn Ingvar Dahlgren

;;; Commentary:
;;
;; Speech-to-Text interface for Emacs using a tcp server (strisper-endpoint)
;; which accepts waveform data recorded by "arecord" and returns timestamped
;; text transcription.
;;
;;; Code:

(defgroup strisper nil
  "Streaming Speech-to-Text using Whisper."
  :group 'external
  :prefix "strisper-")

(defcustom strisper-record-command (format "arecord -f S16_LE -c1 -r 16000 -t raw -D pulse | nc -4 %s 43007" (if (member (getenv "container") '("podman" "docker"))
            "host.docker.internal"
          "localhost"))
  "Shell command used to record audio and pipe it to the whisper server."
  :type 'string
  :group 'strisper)

;; Private state variables
(defvar strisper--target-buffer nil
  "Buffer where `strisper-record-at-point' should insert text.")

(defvar strisper--insert-marker nil
  "Marker tracking where the transcribed text should be inserted.")

(defvar strisper--rec-proc nil
  "The active recording process.")

(defun strisper--process-filter (process string)
  "Handle incoming STRING from the strisper PROCESS."
  (let ((proc-buf (process-buffer process)))
    (when (buffer-live-p proc-buf)
      (with-current-buffer proc-buf
        (let ((moving (= (point) (process-mark process))))
          (save-excursion
            ;; Insert new chunk at the end of process buffer
            (goto-char (process-mark process))
            (insert string)
            (set-marker (process-mark process) (point))
            
            ;; Parse complete lines from the unparsed area
            ;; (FIXED: Properly check if bound and use the variable)
            (unless (boundp 'strisper--parse-pos)
              (setq-local strisper--parse-pos (point-min)))
            (goto-char strisper--parse-pos)
            
            ;; Look for complete lines ending in newline
            (while (re-search-forward "^\\([0-9]+\\)[[:space:]]+\\([0-9]+\\)[[:space:]]+\\(.*\\)\n" nil t)
              (let ((text (match-string 3)))
                ;; Update the parse position so we don't read this line again
                (setq strisper--parse-pos (point))
                
                ;; Insert into target buffer if valid
                (when (and strisper--target-buffer
                           (buffer-live-p strisper--target-buffer)
                           strisper--insert-marker)
                  (with-current-buffer strisper--target-buffer
                    (save-excursion
                      (goto-char strisper--insert-marker)
                      (insert (string-replace "  " " " text) " ")
                      ;; Move marker forward so next insertion appends
                      (set-marker strisper--insert-marker (point))))))))
          ;; Scroll process buffer if user is looking at the end
          (if moving (goto-char (process-mark process))))))))

(defun strisper--start-process (target-buffer)
  "Start the recording process, outputting to TARGET-BUFFER if provided."
  (let ((stdout-buffer (get-buffer-create "*strisper-stdout*"))
        (stderr-buffer (get-buffer-create "*strisper-stderr*")))
    
    ;; Clean up previous buffers so old logs don't mess up parsing
    (with-current-buffer stdout-buffer
      (erase-buffer)
      (setq-local strisper--parse-pos (point-min)))
    
    (setq strisper--target-buffer target-buffer)
    
    ;; Set marker to current point if a target buffer is provided
    (if target-buffer
        (setq strisper--insert-marker (point-marker))
      (setq strisper--insert-marker nil))

    (setq strisper--rec-proc
          (make-process
           :name "strisper--arecord"
           :command (list "sh" "-c" strisper-record-command)
           :buffer stdout-buffer
           :stderr stderr-buffer
           :coding 'utf-8-unix
           :filter #'strisper--process-filter))
    
    (message "Strisper recording started.")))

(defun strisper--ensure-started (target-buffer)
  "Ensure process is started, optionally tied to TARGET-BUFFER."
  (if (process-live-p strisper--rec-proc)
      (when (yes-or-no-p "Already recording, kill old process and restart?")
        (strisper-stop)
        (strisper--start-process target-buffer))
    (strisper--start-process target-buffer)))

;;;###autoload
(defun strisper-record ()
  "Starts the recording process. Output logged to internal buffers only."
  (interactive)
  (strisper--ensure-started nil))

;;;###autoload
(defun strisper-record-at-point ()
  "Starts recording. Inserts processed text safely at the current point."
  (interactive)
  (strisper--ensure-started (current-buffer)))

;;;###autoload
(defun strisper-stop ()
  "Stops the recording process."
  (interactive)
  (if (process-live-p strisper--rec-proc)
      (progn
        (message "Stopping strisper recording process...")
        (kill-process strisper--rec-proc)
        (setq strisper--rec-proc nil)
        (setq strisper--target-buffer nil)
        (when strisper--insert-marker
          (set-marker strisper--insert-marker nil) ; free the marker
          (setq strisper--insert-marker nil))
        (message "Strisper recording process stopped."))
    (message "No active strisper recording process found.")))

(provide 'strisper)
;;; strisper.el ends here
