"""Токен общего бота вводит только автор, в Windows Credential Manager."""
import asyncio
from pathlib import Path
import subprocess
import threading
import sys
import tkinter as tk
from tkinter import ttk,messagebox

ROOT=Path(__file__).resolve().parent
LAUNCHER=ROOT/'telegram-relay.exe'

def main():
    window=tk.Tk()
    window.title('Общий музыкальный бот — настройка автора')
    panel=ttk.Frame(window,padding=20);panel.pack()
    ttk.Label(panel,text='@songrequest_streamer_bot',font=('Segoe UI',14,'bold')).pack(anchor='w')
    ttk.Label(panel,text='Этот токен вводишь только ты. Стримеры подключаются без токена.\nТокен будет сохранён в хранилище паролей Windows.').pack(anchor='w',pady=10)
    entry=ttk.Entry(panel,width=65,show='●');entry.pack(fill='x')
    def paste():
        try:value=window.clipboard_get().strip()
        except tk.TclError:return
        entry.delete(0,'end');entry.insert(0,value)
        return 'break'
    ttk.Button(panel,text='Вставить токен из буфера',command=paste).pack(anchor='w',pady=8)
    entry.bind('<KeyPress>',lambda event:paste() if event.state&4 and event.keycode==86 else None)
    entry.bind('<Shift-Insert>',lambda event:paste())
    note=ttk.Label(panel,text='');note.pack(anchor='w',pady=8)

    async def check(token):
        from aiogram import Bot
        async with Bot(token) as bot:
            me=await bot.get_me()
            if (me.username or '').lower()!='songrequest_streamer_bot':return 'Нужен токен именно @songrequest_streamer_bot.'
            if (await bot.get_webhook_info()).url:return 'У бота уже задан webhook. Подключение не изменено.'
        return ''

    def finish(error,token):
        button.config(state='normal')
        if error:note.config(text=error);return
        saved=subprocess.run([str(LAUNCHER),'--set-token'],input=token.encode(),stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,creationflags=subprocess.CREATE_NO_WINDOW)
        if saved.returncode:note.config(text='Не удалось записать токен в хранилище Windows.');return
        entry.delete(0,'end')
        command=[str(LAUNCHER),str(ROOT/'relay.py'),sys.executable]
        import winreg
        startup=subprocess.list2cmdline(command)
        with winreg.CreateKey(winreg.HKEY_CURRENT_USER,r'Software\Microsoft\Windows\CurrentVersion\Run') as key:
            try:existing=winreg.QueryValueEx(key,'SongrequestCommonBot')[0]
            except FileNotFoundError:existing=None
            if existing is None or existing==startup:winreg.SetValueEx(key,'SongrequestCommonBot',0,winreg.REG_SZ,startup)
        subprocess.Popen(command,cwd=ROOT,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,creationflags=subprocess.CREATE_NO_WINDOW)
        messagebox.showinfo('Настройка сохранена','Запуск общего бота запрошен. Локальный адрес: http://127.0.0.1:8980\n\nДля подключения стримеров с других ПК ещё нужен внешний адрес. Публикация обновления пока не выполняется.',parent=window)
        window.destroy()

    def configure():
        token=entry.get().strip()
        if not token:note.config(text='Вставь токен из BotFather.');return
        button.config(state='disabled');note.config(text='Проверяю Telegram…')
        def worker():
            try:error=asyncio.run(check(token))
            except Exception as exc:error='Проверка не прошла: '+type(exc).__name__
            window.after(0,lambda:finish(error,token))
        threading.Thread(target=worker,daemon=True).start()
    button=ttk.Button(panel,text='Проверить, сохранить и запустить',command=configure);button.pack(anchor='w')
    window.mainloop()

if __name__=='__main__':main()
