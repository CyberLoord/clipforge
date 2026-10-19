import { useId } from "react";
import type { Config, Mode } from "../types";
import { availableModes, modeLabels } from "../modes";

export function ModePicker({
  config,
  value,
  onChange,
  disabled = false,
}: {
  config: Config;
  value: Mode;
  onChange: (mode: Mode) => void;
  disabled?: boolean;
}) {
  const id = useId();
  const available = availableModes(config);
  const hasMontage = available.includes("montage");
  const hasSpeech = available.includes("openai");

  return (
    <fieldset className="mode-picker" disabled={disabled}>
      <legend>Что собрать из видео</legend>
      <label
        className={`mode-option montage-option ${value === "montage" ? "selected" : ""}`}
      >
        <input
          type="radio"
          name={id}
          value="montage"
          checked={value === "montage"}
          onChange={() => onChange("montage")}
          aria-describedby={!hasMontage ? `${id}-setup` : undefined}
        />
        <span>
          <strong>{modeLabels.montage}</strong>
          <span>
            Анализ кадров и монтаж интересных моментов. 2–3 варианта с разным
            темпом и последовательностью сцен.
          </span>
          <span className="edit-styles">
            Динамичный · Кинематографичный · История
          </span>
        </span>
      </label>
      {!hasMontage && value === "montage" && (
        <div className="ai-setup" id={`${id}-setup`} role="status">
          <p>Для создания эдитов подключите OpenAI.</p>
          <details>
            <summary>Как подключить</summary>
            <div>
              <p>
                Укажите ключ в файле <code>.env</code> в корне проекта:
              </p>
              <pre>OPENAI_API_KEY=ваш_ключ</pre>
              <p>Перезапустите сервер и обновите страницу.</p>
              <p>При запуске через Docker:</p>
              <pre>docker compose up -d --force-recreate app</pre>
            </div>
          </details>
        </div>
      )}
      <details className="other-modes" open={value !== "montage" || undefined}>
        <summary>Другие режимы</summary>
        <div className="mode-options">
          <label
            className={`mode-option ${value === "split" ? "selected" : ""}`}
          >
            <input
              type="radio"
              name={id}
              value="split"
              checked={value === "split"}
              onChange={() => onChange("split")}
              disabled={!available.includes("split")}
            />
            <span>
              <strong>{modeLabels.split}</strong>
              <span>
                3 фрагмента по времени. Исходный звук, без анализа содержания и
                субтитров.
              </span>
            </span>
          </label>
          <label
            className={`mode-option ${value === "openai" ? "selected" : ""} ${!hasSpeech ? "unavailable" : ""}`}
          >
            <input
              type="radio"
              name={id}
              value="openai"
              checked={value === "openai"}
              onChange={() => onChange("openai")}
              disabled={!hasSpeech}
            />
            <span>
              <strong>{modeLabels.openai}</strong>
              <span>
                Отдельные отрывки по смыслу речи, с субтитрами.
                {!hasSpeech && " Нужен ключ OpenAI."}
              </span>
            </span>
          </label>
        </div>
        {available.includes("demo") && (
          <label className="demo-option">
            <input
              type="radio"
              name={id}
              value="demo"
              checked={value === "demo"}
              onChange={() => onChange("demo")}
            />
            <span>
              Демо для проверки экспорта. Тестовые субтитры не связаны с речью в
              вашем видео.
            </span>
          </label>
        )}
      </details>
    </fieldset>
  );
}
