interface P {
  a: number;
  b: number;
}

export function main(): i32 {
  const t: P = { a: 0, b: 0 };
  const s: P = { a: 7, b: 9 };
  Object.assign(t, s);
  console.log(t.a);
  console.log(t.b);
  return 0;
}
