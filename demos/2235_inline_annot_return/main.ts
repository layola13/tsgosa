function mk(): { a: number } {
  return { a: 1 };
}

const mkArrow = (): { a: number } => ({ a: 2 });

export function main(): i32 {
  console.log(mk().a);
  console.log(mkArrow().a);
  return 0;
}
