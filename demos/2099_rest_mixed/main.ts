interface M { a: number; r: f64; s: string; arr: number[]; }
function main(): i32 {
  const o: M = { a: 1, r: 2.5, s: "hi", arr: [7, 8] };
  const { a, ...rest } = o;
  console.log(rest.arr[1]);
  console.log(rest.s);
  console.log(a);
  return 0;
}
