interface M { a: number; arr: number[]; }
function main(): i32 {
  const o: M = { a: 1, arr: [7, 8] };
  const { a, ...rest } = o;
  rest.arr.push(9);
  console.log(rest.arr[2]);
  console.log(rest.arr.length);
  return 0;
}
