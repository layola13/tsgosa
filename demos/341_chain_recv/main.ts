function main(): i32 {
  const xs: number[] = [3, 1, 2];
  console.log(xs.filter((x: i32) => x > 1).length);
  const n: number = xs.map((x: i32) => x + 1).filter((x: i32) => x > 2).reduce((a: i32, b: i32) => a + b, 0);
  console.log(n);
  const z: number[] = xs.concat([4]).toReversed();
  console.log(z[0]);
  console.log(xs.slice(-2)[0] + xs.slice(-2)[1]);
  console.log(xs.map((x: i32) => x * 2).some((x: i32) => x > 5));
  xs.map((x: i32) => x + 1).forEach((x: i32) => console.log(x));
  console.log(xs.map((x: i32) => x * 10).find((x: i32) => x > 15));
  return 0;
}
