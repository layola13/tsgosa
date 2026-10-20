interface Inner {
  v: number;
}
interface Outer {
  inner: Inner;
}

function mk(): Inner {
  return { v: 9 };
}

function chainAfter(o: Outer | null): number {
  return o?.inner.v ?? 0;
}

function chainBase(o: Outer): number {
  return o.inner?.v ?? 0;
}

function callBase(): number {
  return mk()?.v ?? 0;
}

export function main(): i32 {
  const o: Outer = { inner: { v: 7 } };
  console.log(chainAfter(o));
  console.log(chainAfter(null));
  console.log(chainBase(o));
  console.log(callBase());
  return 0;
}
