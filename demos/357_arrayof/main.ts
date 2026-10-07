function main(): i32 {
  const a: number[] = Array.of(1, 2);
  console.log(a.length);
  console.log(a[1]);
  const b: number[] = Array.of(7);
  console.log(b.length);
  console.log(b[0]);
  const d: number[] = Array.of();
  console.log(d.length);
  const e: number[] = new Array(1, 2);
  console.log(e.length);
  console.log(e[0]);
  const f: number[] = new Array(5);
  console.log(f.length);
  const g: number[] = new Array();
  console.log(g.length);
  return 0;
}
