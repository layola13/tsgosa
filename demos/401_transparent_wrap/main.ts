interface P {
  a: i32;
}
function mk(): P {
  return { a: 3 };
}
function main(): i32 {
  const p: P = ({ a: 1 }) satisfies P;
  const q: P = mk()!;
  console.log(p.a + q.a);
  return 0;
}
