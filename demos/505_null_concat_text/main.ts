function main(): i32 {
  console.log(null + "x");
  const s: string | null = null;
  console.log(s + "x");
  let u: string | undefined = undefined;
  console.log(u + "x");
  let t: string | null = null;
  t += "y";
  console.log(t);
  return 0;
}
