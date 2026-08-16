// Лентоводец — система резервного копирования на ленточные накопители LTO
// Copyright (C) 2026 AlexRus1234
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

// Команда passwd: bcrypt-хеш пароля для web_password_hash в TOML.

package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
)

// newPasswdCmd — `lentovodec passwd`: спрашивает пароль дважды и
// выводит bcrypt-хеш (SPEC §5). Чтение — из stdin без подавления
// эха (обёртка над терминалом не входит в закреплённые зависимости).
func newPasswdCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "passwd",
		Short: "Сгенерировать bcrypt-хеш пароля для web_password_hash",
		RunE: func(_ *cobra.Command, _ []string) error {
			in := bufio.NewScanner(deps.Stdin)
			fmt.Fprint(deps.Stderr, "пароль: ")
			first, err := readLine(in)
			if err != nil {
				return err
			}
			fmt.Fprint(deps.Stderr, "ещё раз: ")
			second, err := readLine(in)
			if err != nil {
				return err
			}
			if first != second {
				return fmt.Errorf("пароли не совпадают")
			}
			if len(first) == 0 {
				return fmt.Errorf("пустой пароль")
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(first), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("bcrypt: %w", err)
			}
			fmt.Fprintf(deps.Stdout, "web_password_hash = %q\n", string(hash))
			return nil
		},
	}
}

// readLine читает строку и снимает пробелы по краям.
func readLine(in *bufio.Scanner) (string, error) {
	if !in.Scan() {
		if err := in.Err(); err != nil {
			return "", fmt.Errorf("чтение пароля: %w", err)
		}
		return "", fmt.Errorf("ввод закончился")
	}
	return strings.TrimSpace(in.Text()), nil
}
