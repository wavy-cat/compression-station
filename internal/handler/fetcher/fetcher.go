package fetcher

import (
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

// Fetcher - запрашивает контент у origin и возвращает ответ в неизменном виде

func Fetcher(originURL string) fiber.Handler {
	client := &fasthttp.Client{}

	return func(ctx fiber.Ctx) error {
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(resp)

		// Копируем исходный запрос (метод, заголовки, тело)
		ctx.Request().CopyTo(req)

		// Собираем полный URL: origin + тот же URI что пришёл в запросе
		req.SetRequestURI(originURL + string(ctx.Request().RequestURI()))

		// Выполняем запрос
		if err := client.Do(req, resp); err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}

		// Копируем заголовки ответа
		for key, value := range resp.Header.All() {
			ctx.Set(string(key), string(value))
		}

		// Устанавливаем статус и тело ответа
		ctx.Status(resp.StatusCode())
		return ctx.Send(resp.Body())
	}
}
