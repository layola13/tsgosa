interface P {
  x: number;
}

class Counter {
  value: number = 0;
  bump(): number {
    this.value = this.value + 1;
    return this.value;
  }
}

function read(p: P | null): number {
  const c = p;
  return c?.x ?? 0;
}

function readAnn(p: P | null): number {
  const c: P | null = p;
  return c?.x ?? 0;
}

function bumpCopy(c: Counter): number {
  const copy = c;
  return copy.bump();
}

export function main(): i32 {
  const none: P | null = null;
  console.log(read(none));
  console.log(readAnn(none));
  const k = new Counter();
  console.log(bumpCopy(k));
  console.log(k.value);
  return 0;
}
