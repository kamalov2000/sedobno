// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import NotificationsScreen from "./NotificationsScreen";
import NotifyOffer from "../components/NotifyOffer";
import type { NotifySettings } from "../types";
import type { NotifyPatch } from "../api";

// Уведомления (решение владельца 08.10.2026): состояние — с сервера, переключение — только
// по его ответу; предложение после первой записи — по умолчанию ничего не включено.

afterEach(cleanup);
const noop = () => {};
const base: NotifySettings = { tz: "Europe/Moscow", tz_manual: false, reminder_enabled: false, digest_enabled: true, offered: true };

describe("NotificationsScreen", () => {
  it("включение напоминания уходит со временем; экран меняется по ответу сервера", async () => {
    const save = vi.fn(async (b: NotifyPatch) => ({ ...base, reminder_enabled: !!b.reminder_enabled, reminder_time: b.reminder_time }));
    const onChange = vi.fn();
    render(<NotificationsScreen settings={base} onChange={onChange} onBack={noop} save={save} />);
    fireEvent.change(screen.getByLabelText("Время"), { target: { value: "12:30" } });
    fireEvent.click(screen.getByRole("switch", { name: "Напоминание записать еду" }));
    await waitFor(() => expect(onChange).toHaveBeenCalled());
    expect(save).toHaveBeenCalledWith({ reminder_enabled: true, reminder_time: "12:30" });
    expect(onChange.mock.calls[0][0]).toMatchObject({ reminder_enabled: true, reminder_time: "12:30" });
  });

  it("время в тихих часах — можно, с честной пометкой", () => {
    render(<NotificationsScreen settings={base} onChange={noop} onBack={noop} save={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("Время"), { target: { value: "22:00" } });
    expect(screen.getByText(/в тихих часах .* придёт, раз вы выбрали его сами/)).toBeTruthy();
  });

  it("пауза видна и снимается", async () => {
    const save = vi.fn(async () => ({ ...base, reminder_enabled: true, reminder_time: "13:00" }));
    const onChange = vi.fn();
    render(<NotificationsScreen settings={{ ...base, reminder_enabled: true, reminder_time: "13:00", reminder_paused_until: "2026-10-15" }} onChange={onChange} onBack={noop} save={save} />);
    expect(screen.getByText("На паузе до 15.10")).toBeTruthy();
    fireEvent.click(screen.getByText("Снять паузу"));
    await waitFor(() => expect(save).toHaveBeenCalledWith({ resume: true }));
  });

  it("пояс: ручной выбор и возврат к устройству", async () => {
    const save = vi.fn(async (b: NotifyPatch) => ({ ...base, tz: b.tz || "Europe/Moscow", tz_manual: !!b.tz }));
    render(<NotificationsScreen settings={base} onChange={noop} onBack={noop} save={save} />);
    fireEvent.change(screen.getByLabelText("Часовой пояс"), { target: { value: "Asia/Vladivostok" } });
    await waitFor(() => expect(save).toHaveBeenCalledWith({ tz: "Asia/Vladivostok" }));
  });

  it("сбой сохранения — текст ошибки, переключатель не врёт", async () => {
    const save = vi.fn(async () => {
      throw new Error("Выберите время напоминания");
    });
    const onChange = vi.fn();
    render(<NotificationsScreen settings={base} onChange={onChange} onBack={noop} save={save} />);
    fireEvent.click(screen.getByRole("switch", { name: "Итог дня вечером" }));
    expect((await screen.findByRole("alert")).textContent).toBe("Выберите время напоминания");
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("switch", { name: "Итог дня вечером" }).getAttribute("aria-checked")).toBe("true");
  });
});

describe("NotifyOffer — после первой записи", () => {
  it("«Не сейчас» — только отметка показа, ничего не включается", async () => {
    const save = vi.fn(async () => ({ ...base, digest_enabled: false, offered: true }));
    const onDone = vi.fn();
    render(<NotifyOffer onDone={onDone} save={save} />);
    fireEvent.click(screen.getByText("Не сейчас"));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(save).toHaveBeenCalledWith({ offered: true });
  });

  it("«Включить» — напоминание в выбранное время; итог только если отмечен (по умолчанию нет)", async () => {
    const save = vi.fn(async (b: NotifyPatch) => ({ ...base, ...b, offered: true }) as NotifySettings);
    render(<NotifyOffer onDone={noop} save={save} />);
    fireEvent.change(screen.getByLabelText("Время"), { target: { value: "14:00" } });
    fireEvent.click(screen.getByText("Включить"));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][0]).toMatchObject({ offered: true, reminder_enabled: true, reminder_time: "14:00", digest_enabled: false });
  });
});
