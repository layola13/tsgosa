interface P {
  a: number;
  b: number;
}

export function main(): i32 {
  const p: P = { a: 7, b: 9 };
  const es = Object.entries(p);
  console.log(es.length);
  console.log(es[0][0]);
  console.log(es[0][1]);
  console.log(es[1][0]);
  console.log(es[1][1]);
  return 0;
}
