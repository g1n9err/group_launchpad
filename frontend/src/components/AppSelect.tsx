import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from "react";
import { createPortal } from "react-dom";

export type AppSelectOption = { value: string; label: string; disabled?: boolean };

type Props = {
  value: string;
  options: AppSelectOption[];
  onChange: (value: string) => void;
  ariaLabel: string;
  placeholder?: string;
  className?: string;
  menuClassName?: string;
  disabled?: boolean;
};

type PopupPosition = { left: number; top?: number; bottom?: number; width: number; maxHeight: number };

export function AppSelect({ value, options, onChange, ariaLabel, placeholder, className = "", menuClassName = "", disabled }: Props) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const optionRefs = useRef<Array<HTMLDivElement | null>>([]);
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(() => Math.max(0, options.findIndex((option) => option.value === value)));
  const [position, setPosition] = useState<PopupPosition>();
  const selectedIndex = options.findIndex((option) => option.value === value);
  const activeOption = options[activeIndex];
  const selectedLabel = selectedIndex >= 0 ? options[selectedIndex]?.label : placeholder;

  const updatePosition = () => {
    const bounds = trigger.current?.getBoundingClientRect();
    if (!bounds) return;
    const gap = 5;
    const roomBelow = window.innerHeight - bounds.bottom - gap;
    const roomAbove = bounds.top - gap;
    const maxHeight = Math.max(96, Math.min(288, Math.max(roomBelow, roomAbove) - 8));
    const openBelow = roomBelow >= Math.min(220, options.length * 38) || roomBelow >= roomAbove;
    setPosition({
      left: Math.max(8, Math.min(bounds.left, window.innerWidth - bounds.width - 8)),
      width: bounds.width,
      maxHeight,
      ...(openBelow ? { top: Math.min(window.innerHeight - 8, bounds.bottom + gap) } : { bottom: Math.max(8, window.innerHeight - bounds.top + gap) }),
    });
  };

  useLayoutEffect(() => {
    if (!open) return;
    updatePosition();
    const onViewportChange = () => updatePosition();
    window.addEventListener("resize", onViewportChange);
    window.addEventListener("scroll", onViewportChange, true);
    return () => {
      window.removeEventListener("resize", onViewportChange);
      window.removeEventListener("scroll", onViewportChange, true);
    };
  }, [open, options.length]);

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && !trigger.current?.contains(target) && !popup.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("pointerdown", onPointer);
    return () => document.removeEventListener("pointerdown", onPointer);
  }, [open]);

  useEffect(() => {
    if (open) optionRefs.current[activeIndex]?.scrollIntoView({ block: "nearest" });
  }, [activeIndex, open]);

  function show(initialIndex = selectedIndex >= 0 ? selectedIndex : 0) {
    setActiveIndex(initialIndex);
    setOpen(true);
  }

  function choose(option: AppSelectOption) {
    if (option.disabled) return;
    onChange(option.value);
    setOpen(false);
    trigger.current?.focus();
  }

  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!open) { show(); return; }
      const step = event.key === "ArrowDown" ? 1 : -1;
      let next = activeIndex;
      for (let count = 0; count < options.length; count++) {
        next = (next + step + options.length) % options.length;
        if (!options[next]?.disabled) break;
      }
      setActiveIndex(next);
    } else if ((event.key === "Enter" || event.key === " ") && open) {
      event.preventDefault();
      if (activeOption) choose(activeOption);
    } else if ((event.key === "Enter" || event.key === " ") && !open) {
      event.preventDefault(); show();
    } else if (event.key === "Escape" && open) {
      event.preventDefault(); setOpen(false);
    } else if (event.key === "Home" && open) {
      event.preventDefault();
      setActiveIndex(options.findIndex((option) => !option.disabled));
    } else if (event.key === "End" && open) {
      event.preventDefault();
      setActiveIndex(options.findLastIndex((option) => !option.disabled));
    }
  }

  const menu = open && position ? createPortal(
    <div
      id={`${id}-listbox`}
      ref={popup}
      role="listbox"
      aria-label={ariaLabel}
      className={`app-select-menu ${menuClassName}`}
      style={{ position: "fixed", left: position.left, width: position.width, maxHeight: position.maxHeight, top: position.top, bottom: position.bottom }}
    >
      {options.map((option, index) => (
        <div
          key={`${option.value}:${index}`}
          id={`${id}-option-${index}`}
          ref={(element) => { optionRefs.current[index] = element; }}
          role="option"
          aria-selected={option.value === value}
          aria-disabled={option.disabled || undefined}
          className={`app-select-option${option.value === value ? " is-selected" : ""}${index === activeIndex ? " is-active" : ""}${option.disabled ? " is-disabled" : ""}`}
          onMouseEnter={() => setActiveIndex(index)}
          onMouseDown={(event: MouseEvent) => event.preventDefault()}
          onClick={() => choose(option)}
        >
          <span>{option.label}</span>
          {option.value === value && <span className="app-select-check" aria-hidden="true">✓</span>}
        </div>
      ))}
    </div>,
    document.body,
  ) : null;

  return <>
    <button
      ref={trigger}
      type="button"
      role="combobox"
      aria-label={ariaLabel}
      aria-haspopup="listbox"
      aria-expanded={open}
      aria-controls={`${id}-listbox`}
      aria-activedescendant={open ? `${id}-option-${activeIndex}` : undefined}
      className={`app-select-trigger ${className}`}
      disabled={disabled}
      onClick={() => open ? setOpen(false) : show()}
      onKeyDown={onKeyDown}
    >
      <span className={selectedIndex < 0 ? "app-select-placeholder" : "app-select-value"}>{selectedLabel || ""}</span>
      <svg className={`app-select-chevron${open ? " is-open" : ""}`} viewBox="0 0 20 20" fill="none" aria-hidden="true">
        <path d="m5 7.5 5 5 5-5" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </button>
    {menu}
  </>;
}
