class C {
  f: { a: number } = { a: 4 };
}

export function main(): i32 {
  const o: { a: number } = { a: 3 };
  console.log(o.a);
  const c = new C();
  console.log(c.f.a);
  return 0;
}
