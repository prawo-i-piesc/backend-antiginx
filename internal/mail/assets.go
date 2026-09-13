package mail

import _ "embed"

// Logo jedzie razem z wiadomością zamiast wisieć pod adresem: backend stoi w
// sieci wewnętrznej, więc klient pocztowy odbiorcy nie miałby skąd go pobrać.
// Plik jest przeskalowany do dwukrotności rozmiaru wyświetlania, żeby nie
// doklejać kilkudziesięciu kilobajtów do każdego maila.
//
//go:embed assets/logotype.png
var logotypePNG []byte

const logoContentID = "antiginx-logotype"

func logoAttachment() InlineImage {
	return InlineImage{
		ContentID: logoContentID,
		Filename:  "logotype.png",
		MIMEType:  "image/png",
		Content:   logotypePNG,
	}
}
