using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

namespace Gfr.Whisper {
    // Byte-based rotation also bounds a single enormous/no-newline stderr write.
    public sealed class RotatingLog : IDisposable {
        readonly string path;
        readonly long maximum;
        readonly int archives;
        FileStream stream;
        public RotatingLog(string path, long maximum, int archives) {
            if (maximum < 1 || archives < 1 || archives > 10) throw new ArgumentException("Invalid log limits");
            this.path = path; this.maximum = maximum; this.archives = archives;
            Open();
        }
        void Open() {
            RejectLink(path);
            // Enforce changed retention limits even if the current log is empty.
            for (int i = 1; i <= 10; i++) {
                string archive = path + "." + i;
                RejectLink(archive);
                if (!File.Exists(archive)) continue;
                if (i > archives) File.Delete(archive);
                else using (var file = new FileStream(archive, FileMode.Open, FileAccess.Write, FileShare.Read))
                    if (file.Length > maximum) file.SetLength(maximum);
            }
            stream = new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.Read);
            if (stream.Length >= maximum) Rotate();
        }
        static void RejectLink(string value) {
            if (File.Exists(value) && (File.GetAttributes(value) & FileAttributes.ReparsePoint) != 0)
                throw new IOException("Linked log rejected");
        }
        void Rotate() {
            stream.Dispose();
            for (int i = archives; i >= 1; i--) RejectLink(path + "." + i);
            // A lowered configured limit must also bound previously larger logs.
            for (int i = 0; i <= archives; i++) {
                string value = i == 0 ? path : path + "." + i;
                if (File.Exists(value)) using (var file = new FileStream(value, FileMode.Open, FileAccess.Write, FileShare.Read))
                    if (file.Length > maximum) file.SetLength(maximum);
            }
            if (File.Exists(path + "." + archives)) File.Delete(path + "." + archives);
            for (int i = archives - 1; i >= 1; i--)
                if (File.Exists(path + "." + i)) File.Move(path + "." + i, path + "." + (i + 1));
            if (File.Exists(path)) File.Move(path, path + ".1");
            stream = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.Read);
        }
        public void Write(byte[] bytes, int count) {
            int offset = 0;
            while (offset < count) {
                if (stream.Length >= maximum) Rotate();
                int n = (int)Math.Min(count - offset, maximum - stream.Length);
                stream.Write(bytes, offset, n); stream.Flush(); offset += n;
            }
        }
        public void Dispose() { if (stream != null) stream.Dispose(); }
    }

    public static class RuntimeHost {
        const uint Suspended = 4, NoWindow = 0x08000000, UnicodeEnvironment = 0x400;
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] struct Startup {
            public int cb; public string reserved, desktop, title;
            public int x, y, xSize, ySize, xChars, yChars, fill, flags;
            public short show, reservedSize; public IntPtr reservedBytes, input, output, error;
        }
        [StructLayout(LayoutKind.Sequential)] struct ProcessInfo { public IntPtr process, thread; public uint pid, tid; }
        [StructLayout(LayoutKind.Sequential)] struct BasicLimit {
            public long processTime, jobTime; public uint flags; public UIntPtr min, max;
            public uint active; public UIntPtr affinity; public uint priority, scheduling;
        }
        [StructLayout(LayoutKind.Sequential)] struct IoCounters { public ulong a,b,c,d,e,f; }
        [StructLayout(LayoutKind.Sequential)] struct ExtendedLimit {
            public BasicLimit basic; public IoCounters io;
            public UIntPtr processMemory, jobMemory, peakProcess, peakJob;
        }
        [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern IntPtr CreateJobObject(IntPtr attributes, string name);
        [DllImport("kernel32.dll", SetLastError=true)] static extern bool SetInformationJobObject(IntPtr job, int kind, ref ExtendedLimit info, int length);
        [DllImport("kernel32.dll", SetLastError=true)] static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
        [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool CreateProcess(string application, StringBuilder command, IntPtr pa, IntPtr ta, bool inherit, uint flags, IntPtr environment, string directory, ref Startup startup, out ProcessInfo process);
        [DllImport("kernel32.dll")] static extern uint ResumeThread(IntPtr thread);
        [DllImport("kernel32.dll")] static extern uint WaitForSingleObject(IntPtr handle, uint milliseconds);
        [DllImport("kernel32.dll")] static extern bool GetExitCodeProcess(IntPtr process, out uint code);
        [DllImport("kernel32.dll")] static extern bool TerminateProcess(IntPtr process, uint code);
        [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);

        static Task Pump(Stream input, string path, long maximum, int archives) {
            return Task.Run(() => {
                using (var log = new RotatingLog(path, maximum, archives)) {
                    byte[] buffer = new byte[8192]; int count;
                    while ((count = input.Read(buffer, 0, buffer.Length)) > 0) log.Write(buffer, count);
                }
            });
        }
        public static int Run(string executable, string quotedCommand, string ffmpeg, string temp,
            string logs, string statePath, string sid, long maximum, int archives) {
            bool owns = false;
            using (var mutex = new Mutex(false, "Local\\GFRWhisper-" + sid)) {
                try { owns = mutex.WaitOne(0); } catch (AbandonedMutexException) { owns = true; }
                if (!owns) return 0;
                try { return RunOwned(executable, quotedCommand, ffmpeg, temp, logs, statePath, maximum, archives); }
                finally { mutex.ReleaseMutex(); }
            }
        }
        static int RunOwned(string executable, string command, string ffmpeg, string temp,
            string logs, string statePath, long maximum, int archives) {
            IntPtr job = IntPtr.Zero, environment = IntPtr.Zero;
            ProcessInfo child = new ProcessInfo(); Task outputTask = null, errorTask = null;
            using (var stdout = new AnonymousPipeServerStream(PipeDirection.In, HandleInheritability.Inheritable))
            using (var stderr = new AnonymousPipeServerStream(PipeDirection.In, HandleInheritability.Inheritable)) {
                try {
                    job = CreateJobObject(IntPtr.Zero, null);
                    var limits = new ExtendedLimit(); limits.basic.flags = 0x2000; // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
                    if (job == IntPtr.Zero || !SetInformationJobObject(job, 9, ref limits, Marshal.SizeOf(limits))) throw new IOException("Cannot create runtime job");
                    var vars = new SortedDictionary<string,string>(StringComparer.OrdinalIgnoreCase);
                    vars["SystemRoot"] = Environment.GetEnvironmentVariable("SystemRoot");
                    vars["WINDIR"] = vars["SystemRoot"];
                    vars["PATH"] = Path.GetDirectoryName(executable) + ";" + Path.GetDirectoryName(ffmpeg) + ";" + Path.Combine(vars["SystemRoot"], "System32");
                    vars["TEMP"] = temp; vars["TMP"] = temp; vars["AV_LOG_FORCE_NOCOLOR"] = "1";
                    var block = new StringBuilder(); foreach (var pair in vars) block.Append(pair.Key).Append('=').Append(pair.Value).Append('\0'); block.Append('\0');
                    environment = Marshal.StringToHGlobalUni(block.ToString());
                    var startup = new Startup(); startup.cb = Marshal.SizeOf(startup); startup.flags = 0x100;
                    startup.output = stdout.ClientSafePipeHandle.DangerousGetHandle(); startup.error = stderr.ClientSafePipeHandle.DangerousGetHandle();
                    if (!CreateProcess(executable, new StringBuilder(command), IntPtr.Zero, IntPtr.Zero, true,
                        Suspended | NoWindow | UnicodeEnvironment, environment, temp, ref startup, out child)) throw new IOException("Cannot create Whisper process");
                    // Assign BEFORE resume: a killed wrapper cannot orphan a running child.
                    if (!AssignProcessToJobObject(job, child.process)) throw new IOException("Cannot contain Whisper process");
                    stdout.DisposeLocalCopyOfClientHandle(); stderr.DisposeLocalCopyOfClientHandle();
                    using (var process = Process.GetProcessById((int)child.pid)) {
                        File.WriteAllText(statePath, "{\"Pid\":" + child.pid + ",\"StartTicks\":" + process.StartTime.ToUniversalTime().Ticks + "}", new UTF8Encoding(false));
                    }
                    outputTask = Pump(stdout, Path.Combine(logs, "stdout.log"), maximum, archives);
                    errorTask = Pump(stderr, Path.Combine(logs, "stderr.log"), maximum, archives);
                    if (ResumeThread(child.thread) == UInt32.MaxValue) throw new IOException("Cannot resume Whisper process");
                    while (WaitForSingleObject(child.process, 100) == 0x102) {
                        if (outputTask.IsFaulted || errorTask.IsFaulted) throw new IOException("Runtime log write failed");
                    }
                    uint code; if (!GetExitCodeProcess(child.process, out code)) return 1;
                    // Unexpected exit, even code zero, requests Scheduler's bounded restart.
                    return code == 0 ? 1 : (int)code;
                } finally {
                    if (child.process != IntPtr.Zero) TerminateProcess(child.process, 1);
                    if (job != IntPtr.Zero) CloseHandle(job); // also ends any conversion children
                    if (child.thread != IntPtr.Zero) CloseHandle(child.thread);
                    if (child.process != IntPtr.Zero) CloseHandle(child.process);
                    stdout.DisposeLocalCopyOfClientHandle(); stderr.DisposeLocalCopyOfClientHandle();
                    if (environment != IntPtr.Zero) Marshal.FreeHGlobal(environment);
                    if (outputTask != null && errorTask != null) Task.WaitAll(new[] { outputTask, errorTask }, 5000);
                }
            }
        }
    }
}
