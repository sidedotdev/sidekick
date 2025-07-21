<template>
  <div class="grow-wrap" :data-replicated-value="replicatedValue">
    <textarea :disabled="disabled" v-model="textValue" :placeholder="placeholder"></textarea>
  </div>
</template>

<script lang="ts">
import { ref, watch, defineComponent, toRef } from 'vue'

export default defineComponent({
  props: {
    modelValue: {
      type: String,
      default: '',
    },
    placeholder: {
      type: String,
      default: '',
    },
    disabled: {
      type: Boolean,
      default: false,
    },
  },
  setup(props, { emit }) {
    const textValue = ref(props.modelValue)
    const replicatedValue = ref(props.modelValue)

    watch(textValue, (newValue) => {
      replicatedValue.value = newValue
      // Only emit if the new value is different from the current prop value
      // This prevents infinite loops when prop changes update textValue
      if (newValue !== props.modelValue) {
        emit('update:modelValue', newValue)
      }
    })

    watch(toRef(props, 'modelValue'), (newValue) => {
      textValue.value = newValue
      replicatedValue.value = newValue
    })

    return {
      textValue,
      replicatedValue,
      placeholder: toRef(props, 'placeholder'),
      disabled: toRef(props, 'disabled'),
    }
  },
})
</script>

<style scoped>
.grow-wrap {
  /* easy way to plop the elements on top of each other and have them both sized based on the tallest one's height */
  display: grid;
}
.grow-wrap::after {
  /* Note the weird space! Needed to preventy jumpy behavior */
  content: attr(data-replicated-value) ' ';

  /* This is how textarea text behaves */
  white-space: pre-wrap;

  /* Hidden from view, clicks, and screen readers */
  visibility: hidden;
}
.grow-wrap > textarea {
  /* You could leave this, but after a user resizes, then it ruins the auto sizing */
  resize: none;

  /* Firefox shows scrollbar on growth, you can hide like this. */
  overflow: hidden;
}

.grow-wrap > textarea:focus {
  border: 1px solid rgba(131,58,180,1.0) !important;
  outline-color: #ddd;
  outline-style: solid;
}

.grow-wrap > textarea,
.grow-wrap::after {
  /* Identical styling required!! */
  border: 1px solid #888;
  padding: 0.5rem;
  font: inherit;

  /* Place on top of each other */
  grid-area: 1 / 1 / 2 / 2;
}
textarea {
  border-radius: 5px;
}
</style>
