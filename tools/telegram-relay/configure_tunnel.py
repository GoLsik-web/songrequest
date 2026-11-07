"""Один Cloudflare Tunnel для bot.zaluplay.online. Секрет не передаётся в чат."""
from pathlib import Path
import subprocess
import tkinter as tk
from tkinter import ttk,messagebox
import winreg

ROOT=Path(__file__).resolve().parent

def main():
    window=tk.Tk();window.title('Cloudflare Tunnel — bot.zaluplay.online')
    panel=ttk.Frame(window,padding=20);panel.pack()
    ttk.Label(panel,text='Cloudflare Tunnel: bot.zaluplay.online',font=('Segoe UI',13,'bold')).pack(anchor='w')
    ttk.Label(panel,text='Сначала создай Tunnel в Cloudflare и укажи маршрут:\nbot.zaluplay.online → http://127.0.0.1:8980\n\nСюда нужен токен туннеля Cloudflare, не токен Telegram.\nОн сохранится в хранилище паролей Windows.').pack(anchor='w',pady=10)
    entry=ttk.Entry(panel,width=70,show='●');entry.pack(fill='x')
    def paste():
        try:value=window.clipboard_get().strip()
        except tk.TclError:return 'break'
        entry.delete(0,'end');entry.insert(0,value);return 'break'
    ttk.Button(panel,text='Вставить из буфера',command=paste).pack(anchor='w',pady=8)
    entry.bind('<KeyPress>',lambda event:paste() if event.state&4 and event.keycode==86 else None)
    entry.bind('<Shift-Insert>',lambda event:paste())
    note=ttk.Label(panel,text='');note.pack(anchor='w')
    def save():
        value=entry.get().strip()
        if '--token ' in value:value=value.split('--token ',1)[1].split()[0]
        elif 'service install ' in value:value=value.split('service install ',1)[1].split()[0]
        value=value.strip('"\'')
        result=subprocess.run([str(ROOT/'telegram-relay.exe'),'--set-tunnel-token'],input=value.encode(),stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,creationflags=subprocess.CREATE_NO_WINDOW)
        if result.returncode:note.config(text='Не удалось сохранить токен. Скопируй токен туннеля из Cloudflare.');return
        command=[str(ROOT/'telegram-relay.exe'),'--run-tunnel',str(ROOT/'cloudflared.exe')]
        startup=subprocess.list2cmdline(command)
        with winreg.CreateKey(winreg.HKEY_CURRENT_USER,r'Software\Microsoft\Windows\CurrentVersion\Run') as key:
            try:old=winreg.QueryValueEx(key,'SongrequestTelegramTunnel')[0]
            except FileNotFoundError:old=None
            if old and old!=startup:note.config(text='Существующая другая запись автозапуска сохранена без изменений.');return
            winreg.SetValueEx(key,'SongrequestTelegramTunnel',0,winreg.REG_SZ,startup)
        subprocess.Popen(command,cwd=ROOT,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,creationflags=subprocess.CREATE_NO_WINDOW)
        entry.delete(0,'end')
        messagebox.showinfo('Туннель','Запуск запрошен. Проверим https://bot.zaluplay.online/health после активации DNS.',parent=window)
        window.destroy()
    ttk.Button(panel,text='Сохранить и запустить туннель',command=save).pack(anchor='w',pady=10)
    window.mainloop()

if __name__=='__main__':main()
