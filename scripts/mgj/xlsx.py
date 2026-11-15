"""只用标准库写出 excelize 能读取的最小 xlsx。"""

import io
import re
import zipfile
from xml.sax.saxutils import escape

MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
REL_NS = "http://schemas.openxmlformats.org/package/2006/relationships"
OFFICE_REL_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

_ILLEGAL_XML = re.compile(r"[\x00-\x08\x0b\x0c\x0e-\x1f]")
_ATTRIBUTE_ESCAPE = {'"': "&quot;"}


def column_name(index):
    """把从零开始的列号写成 A、B、……、AA。"""
    name = ""
    index += 1
    while index:
        index, remainder = divmod(index - 1, 26)
        name = chr(ord("A") + remainder) + name
    return name


def _text(value):
    text = "" if value is None else str(value)
    return _ILLEGAL_XML.sub("", text)


def _sheet_xml(rows):
    parts = [
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>',
        f'<worksheet xmlns="{MAIN_NS}"><sheetData>',
    ]
    for row_index, row in enumerate(rows, start=1):
        parts.append(f'<row r="{row_index}">')
        for column_index, value in enumerate(row):
            ref = f"{column_name(column_index)}{row_index}"
            text = escape(_text(value))
            parts.append(
                f'<c r="{ref}" t="inlineStr"><is><t xml:space="preserve">{text}</t></is></c>'
            )
        parts.append("</row>")
    parts.append("</sheetData></worksheet>")
    return "".join(parts)


def _workbook_xml(sheets):
    entries = []
    for index, (name, _headers, _rows) in enumerate(sheets, start=1):
        entries.append(
            f'<sheet name="{escape(_text(name), _ATTRIBUTE_ESCAPE)}" sheetId="{index}" r:id="rId{index}"/>'
        )
    return (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        f'<workbook xmlns="{MAIN_NS}" xmlns:r="{OFFICE_REL_NS}"><sheets>'
        + "".join(entries)
        + "</sheets></workbook>"
    )


def _workbook_rels_xml(sheets):
    entries = []
    for index in range(1, len(sheets) + 1):
        entries.append(
            f'<Relationship Id="rId{index}" Type="{OFFICE_REL_NS}/worksheet" '
            f'Target="worksheets/sheet{index}.xml"/>'
        )
    entries.append(
        f'<Relationship Id="rId{len(sheets) + 1}" Type="{OFFICE_REL_NS}/styles" '
        'Target="styles.xml"/>'
    )
    return (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        f'<Relationships xmlns="{REL_NS}">' + "".join(entries) + "</Relationships>"
    )


def _content_types_xml(sheets):
    overrides = [
        '<Override PartName="/xl/workbook.xml" '
        'ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>'
    ]
    for index in range(1, len(sheets) + 1):
        overrides.append(
            f'<Override PartName="/xl/worksheets/sheet{index}.xml" '
            'ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>'
        )
    overrides.append(
        '<Override PartName="/xl/styles.xml" '
        'ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>'
    )
    return (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
        '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
        '<Default Extension="xml" ContentType="application/xml"/>'
        + "".join(overrides)
        + "</Types>"
    )


def _root_rels_xml():
    return (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        f'<Relationships xmlns="{REL_NS}"><Relationship Id="rId1" '
        f'Type="{OFFICE_REL_NS}/officeDocument" Target="xl/workbook.xml"/></Relationships>'
    )


def _styles_xml():
    return (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        f'<styleSheet xmlns="{MAIN_NS}">'
        '<fonts count="1"><font><sz val="11"/><name val="Calibri"/></font></fonts>'
        '<fills count="2"><fill><patternFill patternType="none"/></fill>'
        '<fill><patternFill patternType="gray125"/></fill></fills>'
        '<borders count="1"><border/></borders>'
        '<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>'
        '<cellXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/></cellXfs>'
        '<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>'
        "</styleSheet>"
    )


def workbook_bytes(sheets):
    """把 [(表名, 表头, 数据行)] 写成字节；时间戳固定，重复运行字节一致。"""
    entries = [
        ("[Content_Types].xml", _content_types_xml(sheets)),
        ("_rels/.rels", _root_rels_xml()),
        ("xl/workbook.xml", _workbook_xml(sheets)),
        ("xl/_rels/workbook.xml.rels", _workbook_rels_xml(sheets)),
        ("xl/styles.xml", _styles_xml()),
    ]
    for index, (_name, headers, rows) in enumerate(sheets, start=1):
        entries.append((f"xl/worksheets/sheet{index}.xml", _sheet_xml([headers] + rows)))
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w", compression=zipfile.ZIP_STORED) as archive:
        for name, content in entries:
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.create_system = 0
            info.compress_type = zipfile.ZIP_STORED
            info.external_attr = 0o600 << 16
            archive.writestr(info, content.encode("utf-8"))
    return buffer.getvalue()


def write_workbook(path, sheets):
    """写出 xlsx；父目录不存在时创建。"""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(workbook_bytes(sheets))
