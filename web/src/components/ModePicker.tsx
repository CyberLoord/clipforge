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
  const hasAI = available.includes("openai");

  return (
    <fieldset className="mode-picker" disabled={disabled}>
      <legend>Как нарезать видео</legend>
      <div className="mode-options">
        <label className={`mode-option ${value === "split" ? "selected" : ""}`}>
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
              3 фрагмента по времени. Исходный звук, без расшифровки и
              субтитров.
            </span>
          </span>
        </label>
        <label
          className={`mode-option ${value === "openai" ? "selected" : ""} ${!hasAI ? "unavailable" : ""}`}
        >
          <input
            type="radio"
            name={id}
            value="openai"
            checked={value === "openai"}
            onChange={() => onChange("openai")}
            disabled={!hasAI}
            aria-describedby={!hasAI ? `${id}-setup` : undefined}
          />
          <span>
            <strong>{modeLabels.openai}</strong>
            <span>Распознавание речи, выбор по смыслу и субтитры.</span>
          </span>
        </label>
      </div>
      {!hasAI && (
        <details className="ai-setup" id={`${id}-setup`}>
          <summary>
            Для анализа речи нужен ключ OpenAI. <span>Настройка</span>
          </summary>
          <div>
            <p>
              Укажите ключ в файле <code>.env</code> в корне проекта:
            </p>
            <pre>OPENAI_API_KEY=ваш_ключ</pre>
            <p>Перезапустите приложение, чтобы применить переменную:</p>
            <pre>docker compose up -d --force-recreate app</pre>
            <p>
              При запуске без Docker перезапустите сервер с обновлённым
              окружением. Затем обновите страницу.
            </p>
          </div>
        </details>
      )}
      {available.includes("demo") && (
        <details className="demo-option" open={value === "demo" || undefined}>
          <summary>Демо для проверки</summary>
          <label>
            <input
              type="radio"
              name={id}
              value="demo"
              checked={value === "demo"}
              onChange={() => onChange("demo")}
            />
            <span>
              Готовый тестовый текст и субтитры, не связанные с речью в вашем
              видео.
            </span>
          </label>
        </details>
      )}
    </fieldset>
  );
}
