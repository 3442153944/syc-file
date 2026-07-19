// previewUtil/OfficeExtractor.kt
// 职责：用 Apache POI 把已下载到本地的 Office 文档解析为可在 Compose 中渲染的结构化内容。
// 定位：应用内"基础"预览（文本/表格保真，非版面级还原）；POI 读写皆备，为后续在线编辑预留基础。
// 健壮性：所有 POI 调用集中在此并全程 try/catch（含 LinkageError/NoClassDefFoundError，规避 Android 上
//        个别代码路径缺类导致的崩溃），失败回退为 Result.failure，界面显示"无法解析"而非崩溃。
package com.sunyuanling.filesync.previewUtil

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.apache.poi.hwpf.extractor.WordExtractor
import org.apache.poi.sl.usermodel.SlideShowFactory
import org.apache.poi.sl.usermodel.TextShape
import org.apache.poi.ss.usermodel.DataFormatter
import org.apache.poi.ss.usermodel.WorkbookFactory
import org.apache.poi.xwpf.usermodel.XWPFDocument
import org.apache.poi.xwpf.usermodel.XWPFParagraph
import org.apache.poi.xwpf.usermodel.XWPFTable
import java.io.File

// ==================== 渲染数据模型 ====================

sealed interface OfficeContent {
    data class Word(val blocks: List<WordBlock>) : OfficeContent
    data class Excel(val sheets: List<ExcelSheet>) : OfficeContent
    data class Ppt(val slides: List<PptSlide>) : OfficeContent
}

sealed interface WordBlock {
    data class Para(val text: String) : WordBlock
    data class Table(val rows: List<List<String>>) : WordBlock
}

data class ExcelSheet(val name: String, val rows: List<List<String>>, val truncated: Boolean)

data class PptSlide(val index: Int, val lines: List<String>)

object OfficeExtractor {

    // 预览保护上限，避免超大表格/文档 OOM
    private const val MAX_EXCEL_ROWS = 500
    private const val MAX_EXCEL_COLS = 50

    /**
     * 解析本地 Office 文件。[type] 须为 OFFICE_WORD/OFFICE_EXCEL/OFFICE_PPT 之一。
     */
    suspend fun extract(file: File, type: PreviewType): Result<OfficeContent> =
        withContext(Dispatchers.Default) {
            try {
                val content = when (type) {
                    PreviewType.OFFICE_WORD -> extractWord(file)
                    PreviewType.OFFICE_EXCEL -> extractExcel(file)
                    PreviewType.OFFICE_PPT -> extractPpt(file)
                    else -> return@withContext Result.failure(IllegalArgumentException("非 Office 类型：$type"))
                }
                Result.success(content)
            } catch (e: CancellationException) {
                throw e
            } catch (t: Throwable) {
                // 包含 POI 在 Android 上可能抛出的 LinkageError / NoClassDefFoundError
                Result.failure(Exception("无法解析该文档：${t.message ?: t.javaClass.simpleName}", t))
            }
        }

    /** OOXML（docx/xlsx/pptx）为 ZIP，头两字节为 'P''K'；旧二进制 OLE2 则否。 */
    private fun isZip(file: File): Boolean = file.inputStream().use { input ->
        val b0 = input.read()
        val b1 = input.read()
        b0 == 0x50 && b1 == 0x4B
    }

    // -------------------- Word --------------------

    private fun extractWord(file: File): OfficeContent.Word {
        val blocks = if (isZip(file)) extractDocx(file) else extractDoc(file)
        return OfficeContent.Word(blocks)
    }

    private fun extractDocx(file: File): List<WordBlock> {
        val blocks = mutableListOf<WordBlock>()
        file.inputStream().use { input ->
            XWPFDocument(input).use { doc ->
                for (element in doc.bodyElements) {
                    when (element) {
                        is XWPFParagraph -> {
                            val text = element.text
                            if (text.isNotBlank()) blocks.add(WordBlock.Para(text))
                        }
                        is XWPFTable -> {
                            val rows = element.rows.map { row ->
                                row.tableCells.map { it.text.orEmpty() }
                            }
                            if (rows.isNotEmpty()) blocks.add(WordBlock.Table(rows))
                        }
                    }
                }
            }
        }
        return blocks
    }

    private fun extractDoc(file: File): List<WordBlock> {
        file.inputStream().use { input ->
            WordExtractor(input).use { extractor ->
                return extractor.paragraphText
                    .map { it.trimEnd('\r', '\n') }
                    .filter { it.isNotBlank() }
                    .map { WordBlock.Para(it) }
            }
        }
    }

    // -------------------- Excel --------------------

    private fun extractExcel(file: File): OfficeContent.Excel {
        val formatter = DataFormatter()
        val sheets = mutableListOf<ExcelSheet>()
        file.inputStream().use { input ->
            WorkbookFactory.create(input).use { wb ->
                for (s in 0 until wb.numberOfSheets) {
                    val sheet = wb.getSheetAt(s)
                    val rows = mutableListOf<List<String>>()
                    var truncated = false
                    val lastRow = sheet.lastRowNum
                    for (r in 0..lastRow) {
                        if (rows.size >= MAX_EXCEL_ROWS) { truncated = true; break }
                        val row = sheet.getRow(r)
                        if (row == null) { rows.add(emptyList()); continue }
                        val lastCol = minOf(row.lastCellNum.toInt(), MAX_EXCEL_COLS)
                        if (row.lastCellNum > MAX_EXCEL_COLS) truncated = true
                        val cells = ArrayList<String>(maxOf(lastCol, 0))
                        for (c in 0 until maxOf(lastCol, 0)) {
                            val cell = row.getCell(c)
                            cells.add(if (cell == null) "" else formatter.formatCellValue(cell))
                        }
                        rows.add(cells)
                    }
                    sheets.add(ExcelSheet(name = sheet.sheetName, rows = rows, truncated = truncated))
                }
            }
        }
        return OfficeContent.Excel(sheets)
    }

    // -------------------- PPT --------------------

    private fun extractPpt(file: File): OfficeContent.Ppt {
        val slides = mutableListOf<PptSlide>()
        file.inputStream().use { input ->
            val show = SlideShowFactory.create(input)
            show.use { slideShow ->
                slideShow.slides.forEachIndexed { index, slide ->
                    val lines = mutableListOf<String>()
                    for (shape in slide.shapes) {
                        if (shape is TextShape<*, *>) {
                            val text = shape.text
                            if (!text.isNullOrBlank()) {
                                text.split('\r', '\n')
                                    .map { it.trim() }
                                    .filter { it.isNotEmpty() }
                                    .forEach { lines.add(it) }
                            }
                        }
                    }
                    slides.add(PptSlide(index = index + 1, lines = lines))
                }
            }
        }
        return OfficeContent.Ppt(slides)
    }
}
