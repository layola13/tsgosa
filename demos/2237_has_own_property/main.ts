interface P {
  a: number;
  b: number;
}

export function main(): i32 {
  const p: P = { a: 1, b: 2 };
  console.log(p.hasOwnProperty("a") ? 1 : 0);
  console.log(p.hasOwnProperty("z") ? 1 : 0);
  console.log(Object.hasOwn(p, "b") ? 1 : 0);
  return 0;
}
