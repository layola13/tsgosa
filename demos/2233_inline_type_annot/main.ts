function read(o: { a: number }): number {
  return o.a;
}

export function main(): i32 {
  console.log(read({ a: 3 }));
  return 0;
}
