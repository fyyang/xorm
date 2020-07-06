// Copyright 2016 The Xorm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xorm

import (
	"strings"
	"xorm.io/xorm/schemas"
)

type TableStruct struct {
	*schemas.Table
	Columns    []*schemas.Column
}

func (session *Session) createTable3(bean *schemas.Table) error {

	session.statement.RefTable = bean
	session.statement.SetTableName(bean.Name)

	sqlStrs := session.statement.GenCreateTableSQL()
	for _, s := range sqlStrs {
		_, err := session.exec(s)
		if err != nil {
			return err
		}
	}
	return nil
}

func (session *Session) createUniques3(bean *schemas.Table) error {

	session.statement.RefTable = bean
	session.statement.SetTableName(bean.Name)

	sqls := session.statement.GenUniqueSQL()
	for _, sqlStr := range sqls {
		_, err := session.exec(sqlStr)
		if err != nil {
			return err
		}
	}
	return nil
}

func (session *Session) createIndexes3(bean *schemas.Table) error {

	session.statement.RefTable = bean
	session.statement.SetTableName(bean.Name)

	sqls := session.statement.GenIndexSQL()
	for _, sqlStr := range sqls {
		_, err := session.exec(sqlStr)
		if err != nil {
			return err
		}
	}
	return nil
}

// Sync3 synchronize structs to database tables
func (session *Session) Sync3(beans ...interface{}) error {
	engine := session.engine

	if session.isAutoClose {
		session.isAutoClose = false
		defer session.Close()
	}

	tables, err := engine.dialect.GetTables(session.getQueryer(), session.ctx)
	if err != nil {
		return err
	}

	session.autoResetStatement = false
	defer func() {
		session.autoResetStatement = true
		session.resetStatement()
	}()

	for _, bean := range beans {
		table := bean.(*schemas.Table)
		var tbName string
		if len(session.statement.AltTableName) > 0 {
			tbName = session.statement.AltTableName
		} else {
			tbName = table.Name
		}
		tbNameWithSchema := engine.tbNameWithSchema(tbName)



		var oriTable *schemas.Table
		for _, tb := range tables {
			if strings.EqualFold(engine.tbNameWithSchema(tb.Name), engine.tbNameWithSchema(tbName)) {
				oriTable = tb
				break
			}
		}

		// this is a new table
		if oriTable == nil {
			err = session.StoreEngine(session.statement.StoreEngine).createTable3(table)
			if err != nil {
				return err
			}

			err = session.createUniques3(table)
			if err != nil {
				return err
			}

			err = session.createIndexes3(table)
			if err != nil {
				return err
			}
			continue
		}

		// this will modify an old table
		if err = engine.loadTableInfo(oriTable); err != nil {
			return err
		}

		// check columns
		for _, col := range table.Columns() {
			var oriCol *schemas.Column
			for _, col2 := range oriTable.Columns() {
				if strings.EqualFold(col.Name, col2.Name) {
					oriCol = col2
					break
				}
			}

			// column is not exist on table
			if oriCol == nil {
				session.statement.RefTable = table
				session.statement.SetTableName(tbNameWithSchema)
				if err = session.addColumn(col.Name); err != nil {
					return err
				}
				continue
			}

			err = nil
			expectedType := engine.dialect.SQLType(col)
			curType := engine.dialect.SQLType(oriCol)
			if expectedType != curType {
				if expectedType == schemas.Text &&
					strings.HasPrefix(curType, schemas.Varchar) {
					// currently only support mysql & postgres
					if engine.dialect.URI().DBType == schemas.MYSQL ||
						engine.dialect.URI().DBType == schemas.POSTGRES {
						engine.logger.Infof("Table %s column %s change type from %s to %s\n",
							tbNameWithSchema, col.Name, curType, expectedType)
						_, err = session.exec(engine.dialect.ModifyColumnSQL(tbNameWithSchema, col))
					} else {
						engine.logger.Warnf("Table %s column %s db type is %s, struct type is %s\n",
							tbNameWithSchema, col.Name, curType, expectedType)
					}
				} else if strings.HasPrefix(curType, schemas.Varchar) && strings.HasPrefix(expectedType, schemas.Varchar) {
					if engine.dialect.URI().DBType == schemas.MYSQL {
						if oriCol.Length < col.Length {
							engine.logger.Infof("Table %s column %s change type from varchar(%d) to varchar(%d)\n",
								tbNameWithSchema, col.Name, oriCol.Length, col.Length)
							_, err = session.exec(engine.dialect.ModifyColumnSQL(tbNameWithSchema, col))
						}
					}
				} else {
					if !(strings.HasPrefix(curType, expectedType) && curType[len(expectedType)] == '(') {
						engine.logger.Warnf("Table %s column %s db type is %s, struct type is %s",
							tbNameWithSchema, col.Name, curType, expectedType)
						_, err = session.exec(engine.dialect.ModifyColumnSQL(tbNameWithSchema, col))
					}
				}
			} else if expectedType == schemas.Varchar {
				if engine.dialect.URI().DBType == schemas.MYSQL {
					if oriCol.Length < col.Length {
						engine.logger.Infof("Table %s column %s change type from varchar(%d) to varchar(%d)\n",
							tbNameWithSchema, col.Name, oriCol.Length, col.Length)
						_, err = session.exec(engine.dialect.ModifyColumnSQL(tbNameWithSchema, col))
					}
				}
			}

			if col.Default != oriCol.Default {
				switch {
				case col.IsAutoIncrement: // For autoincrement column, don't check default
				case (col.SQLType.Name == schemas.Bool || col.SQLType.Name == schemas.Boolean) &&
					((strings.EqualFold(col.Default, "true") && oriCol.Default == "1") ||
						(strings.EqualFold(col.Default, "false") && oriCol.Default == "0")):
				default:
					engine.logger.Warnf("Table %s Column %s db default is %s, struct default is %s",
						tbName, col.Name, oriCol.Default, col.Default)
					_, err = session.exec(engine.dialect.ModifyColumnSQL(tbNameWithSchema, col))
				}
			}
			if col.Nullable != oriCol.Nullable {
				engine.logger.Warnf("Table %s Column %s db nullable is %v, struct nullable is %v",
					tbName, col.Name, oriCol.Nullable, col.Nullable)
				_, err = session.exec(engine.dialect.ModifyColumnSQL(tbNameWithSchema, col))
			}

			if err != nil {
				return err
			}
		}

		var foundIndexNames = make(map[string]bool)
		var addedNames = make(map[string]*schemas.Index)

		for name, index := range table.Indexes {
			var oriIndex *schemas.Index
			for name2, index2 := range oriTable.Indexes {
				if index.Equal(index2) {
					oriIndex = index2
					foundIndexNames[name2] = true
					break
				}
			}

			if oriIndex != nil {
				if oriIndex.Type != index.Type {
					sql := engine.dialect.DropIndexSQL(tbNameWithSchema, oriIndex)
					_, err = session.exec(sql)
					if err != nil {
						return err
					}
					oriIndex = nil
				}
			}

			if oriIndex == nil {
				addedNames[name] = index
			}
		}

		for name2, index2 := range oriTable.Indexes {
			if _, ok := foundIndexNames[name2]; !ok {
				sql := engine.dialect.DropIndexSQL(tbNameWithSchema, index2)
				_, err = session.exec(sql)
				if err != nil {
					return err
				}
			}
		}

		for name, index := range addedNames {
			if index.Type == schemas.UniqueType {
				session.statement.RefTable = table
				session.statement.SetTableName(tbNameWithSchema)
				err = session.addUnique(tbNameWithSchema, name)
			} else if index.Type == schemas.IndexType {
				session.statement.RefTable = table
				session.statement.SetTableName(tbNameWithSchema)
				err = session.addIndex(tbNameWithSchema, name)
			}
			if err != nil {
				return err
			}
		}

		// check all the columns which removed from struct fields but left on database tables.
		for _, colName := range oriTable.ColumnsSeq() {
			if table.GetColumn(colName) == nil {
				engine.logger.Warnf("Table %s has column %s but struct has not related field", engine.TableName(oriTable.Name, true), colName)
			}
		}
	}

	return nil
}