type T = { a: number };
function main(): number {
  const o = { a: 1 } satisfies T;
  const q = { a: 2 } as T;
  console.log(o.a);
  console.log(q.a);
  return o.a + q.a * 10;
}
console.log(main());
