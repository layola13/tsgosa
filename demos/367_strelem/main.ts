function main(): i32 {
  const a: string[] = ["official", "x"];
  const c = a[1];
  console.log(c);
  console.log(a[1]);
  console.log(c.length);
  console.log(c + "!");
  if (c == "x") {
    console.log(1);
  }
  return 0;
}
