import { Icon } from "@iconify/react";

export const IconRoute = (p: { className?: string }) => (
  <svg className={p.className} viewBox="0 0 14 20" fill="currentColor" xmlns="http://www.w3.org/2000/svg" fillRule="evenodd" aria-hidden="true">
    <path d="M10.008,12.17 C10.008,13.275 9.108,14.17 8,14.17 C6.89,14.17 5.992,13.275 5.992,12.17 C5.992,11.065 6.89,10.17 8,10.17 C9.108,10.17 10.008,11.065 10.008,12.17 M7.973,18.005 C5.39,18.005 3.035,16.295 2.239,13.848 C1.446,11.41 2.358,8.739 4.344,7.227 C4.894,6.808 5.095,6.113 5.005,5.428 C4.781,3.732 6.099,2 7.973,2 C9.846,2 11.164,3.732 10.94,5.428 C10.85,6.112 11.051,6.808 11.601,7.227 C13.586,8.739 14.499,11.41 13.705,13.848 C12.91,16.295 10.555,18.005 7.973,18.005 M13.316,6.039 C13.076,5.823 12.955,5.519 12.968,5.198 C13.075,2.432 10.833,0 7.973,0 C5.111,0 2.868,2.433 2.977,5.2 C2.989,5.52 2.869,5.824 2.629,6.038 C-1.615,9.817 -0.632,17.124 4.94,19.416 C7.89,20.629 11.377,19.909 13.631,17.658 C17.125,14.17 16.528,8.905 13.316,6.039" />
  </svg>
);
export const IconHome = (p: { className?: string }) => <Icon icon="gravity-ui:house" className={p.className} />;
export const IconServer = (p: { className?: string }) => <Icon icon="gravity-ui:server" className={p.className} />;
export const IconLayers = (p: { className?: string }) => <Icon icon="gravity-ui:layers" className={p.className} />;
export const IconBox = (p: { className?: string }) => <Icon icon="gravity-ui:box" className={p.className} />;
export const IconKey = (p: { className?: string }) => <Icon icon="gravity-ui:key" className={p.className} />;
export const IconActivity = (p: { className?: string }) => <Icon icon="mdi:clipboard-text-outline" className={p.className} />;
export const IconGauge = (p: { className?: string }) => <Icon icon="mdi:chart-line" className={p.className} />;
export const IconChat = (p: { className?: string }) => <Icon icon="mdi:message-text-outline" className={p.className} />;
export const IconLogout = (p: { className?: string }) => <Icon icon="gravity-ui:sign-out" className={p.className} />;

export const IconPlus = (p: { className?: string }) => <Icon icon="gravity-ui:plus" className={p.className} />;
export const IconPencil = (p: { className?: string }) => <Icon icon="gravity-ui:pencil" className={p.className} />;
export const IconTrash = (p: { className?: string }) => <Icon icon="gravity-ui:trash-bin" className={p.className} />;
export const IconArrow = (p: { className?: string; dir?: "up" | "down" }) => <Icon icon="gravity-ui:chevron-up" className={`${p.className ?? ""} ${p.dir === "down" ? "rotate-180" : ""}`} />;
export const IconX = (p: { className?: string }) => <Icon icon="gravity-ui:x" className={p.className} />;
export const IconSparkles = (p: { className?: string }) => <Icon icon="gravity-ui:sparkles" className={p.className} />;
export const IconStack = (p: { className?: string }) => <Icon icon="gravity-ui:stack" className={p.className} />;
export const IconSearch = (p: { className?: string }) => <Icon icon="gravity-ui:magnifier" className={p.className} />;
export const IconPower = (p: { className?: string }) => <Icon icon="gravity-ui:power" className={p.className} />;
export const IconDotsVertical = (p: { className?: string }) => <Icon icon="mdi:dots-vertical" className={p.className} />;
export const IconDollar = (p: { className?: string }) => <Icon icon="mdi:currency-usd" className={p.className} />;
export const IconStop = (p: { className?: string }) => <Icon icon="gravity-ui:stop" className={p.className} />;
export const IconArrowUp = (p: { className?: string }) => <Icon icon="gravity-ui:arrow-up" className={p.className} />;
export const IconCopy = (p: { className?: string }) => <Icon icon="gravity-ui:copy" className={p.className} />;
export const IconCheck = (p: { className?: string }) => <Icon icon="gravity-ui:check" className={p.className} />;
export const IconApi = (p: { className?: string }) => <Icon icon="gravity-ui:api" className={p.className} />;
export const IconChevron = (p: { className?: string }) => <Icon icon="gravity-ui:chevron-right" className={p.className} />;
export const IconEye = (p: { className?: string }) => <Icon icon="gravity-ui:eye" className={p.className} />;
export const IconEyeOff = (p: { className?: string }) => <Icon icon="gravity-ui:eye-closed" className={p.className} />;
export const IconCalendar = (p: { className?: string }) => <Icon icon="gravity-ui:calendar" className={p.className} />;