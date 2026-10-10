function main(): i32 {
  const w: string = "cat";
  let r = "";
  switch (w) { case "cat": r = "meow"; break; case "dog": r = "woof"; break; default: r = "?"; }
  console.log(r);
  return 0;
}
