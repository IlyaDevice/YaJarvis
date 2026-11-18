local endpoints = {
  DeepSeek = "https://api.deepseek.com/chat/completions",
  OpenRouter = "https://openrouter.ai/api/v1/chat/completions",
}

return {
  chat = function(request)
    local settings = jarvis.settings
    local url = assert(endpoints[settings.provider], "Выберите сервис в настройках плагина")
    local model = assert(settings.model, "Укажите ID модели в настройках плагина")
    local key = assert(settings.key, "Укажите ключ API в настройках плагина")
    assert(model ~= "" and key ~= "", "Укажите ID модели и ключ API")
    local body = { model = model, messages = request.messages, stream = false }
    if settings.provider == "DeepSeek" then
      body.thinking = { type = "disabled" }
    end
    if #request.tools > 0 then body.tools = request.tools end
    local response = jarvis.fetch(url, {
      method = "POST",
      headers = { ["Content-Type"] = "application/json", Authorization = "Bearer " .. key },
      body = jarvis.json.encode(body),
    })
    assert(response.ok, "API вернул HTTP " .. response.status .. ". Проверьте ключ, модель и лимиты")
    local result = jarvis.json.decode(response.body)
    local message = assert(result.choices and result.choices[1] and result.choices[1].message, "API не вернул ответ модели")
    local calls = {}
    for _, call in ipairs(type(message.tool_calls) == "table" and message.tool_calls or {}) do
      local fn = call["function"]
      calls[#calls + 1] = { id = call.id, name = fn.name, arguments = fn.arguments }
    end
    local text = type(message.content) == "string" and message.content or ""
    return #calls == 0 and text or { text = text, tool_calls = calls }
  end,
}
