package com.example.app.core.data

import com.example.app.core.remote.NullAsEmptyListSerializer
import kotlinx.serialization.DeserializationStrategy
import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.descriptors.buildClassSerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonContentPolymorphicSerializer
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonEncoder
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

private const val EVENT_TYPE_KEY = "eventType"

/**
 * Messages from the flow events websocket, discriminated by `eventType`.
 * Event types the app does not model decode to [Unknown] instead of failing,
 * so the server can add events without breaking older clients.
 */
@Serializable(with = FlowEventSerializer::class)
sealed interface FlowEvent {
    @Serializable
    data class ChatMessageDelta(
        val flowActionId: String,
        val chatMessageDelta: ChatMessageDeltaPayload = ChatMessageDeltaPayload(),
    ) : FlowEvent

    /**
     * Either the flow's own status (`parentId` is the flow and `targetId` is
     * blank) or a subflow's status, identified by `targetId` or, in the legacy
     * shape, by `parentId`.
     */
    @Serializable
    data class StatusChange(
        val parentId: String = "",
        val targetId: String = "",
        val status: String = "",
    ) : FlowEvent

    @Serializable
    data class ProgressText(
        val parentId: String = "",
        val text: String = "",
        val details: String = "",
    ) : FlowEvent

    @Serializable
    data class EndStream(val parentId: String = "") : FlowEvent

    data class Unknown(val eventType: String, val raw: JsonObject) : FlowEvent
}

@Serializable
data class ChatMessageDeltaPayload(
    val role: String = "",
    val content: String = "",
    @Serializable(with = NullAsEmptyListSerializer::class)
    val toolCalls: List<ToolCallDelta> = emptyList(),
)

@Serializable
data class ToolCallDelta(
    val id: String = "",
    val name: String = "",
    val arguments: String = "",
)

object FlowEventSerializer : JsonContentPolymorphicSerializer<FlowEvent>(FlowEvent::class) {
    override fun selectDeserializer(element: JsonElement): DeserializationStrategy<FlowEvent> =
        when (element.jsonObject[EVENT_TYPE_KEY]?.jsonPrimitive?.contentOrNull) {
            "chat_message_delta" -> FlowEvent.ChatMessageDelta.serializer()
            "status_change" -> FlowEvent.StatusChange.serializer()
            "progress_text" -> FlowEvent.ProgressText.serializer()
            "end_stream" -> FlowEvent.EndStream.serializer()
            else -> UnknownFlowEventSerializer
        }
}

private object UnknownFlowEventSerializer : KSerializer<FlowEvent.Unknown> {
    override val descriptor: SerialDescriptor = buildClassSerialDescriptor("FlowEvent.Unknown")

    override fun deserialize(decoder: Decoder): FlowEvent.Unknown {
        val jsonDecoder = decoder as? JsonDecoder
            ?: throw SerializationException("unknown flow events can only be decoded from JSON")
        val raw = jsonDecoder.decodeJsonElement().jsonObject
        return FlowEvent.Unknown(raw[EVENT_TYPE_KEY]?.jsonPrimitive?.contentOrNull ?: "", raw)
    }

    override fun serialize(encoder: Encoder, value: FlowEvent.Unknown) {
        val jsonEncoder = encoder as? JsonEncoder
            ?: throw SerializationException("unknown flow events can only be encoded to JSON")
        jsonEncoder.encodeJsonElement(value.raw)
    }
}