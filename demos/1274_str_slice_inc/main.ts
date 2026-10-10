function main(): i32 {
  const s: string = "hello";
  console.log(s.slice(1, 3));
  console.log(s.includes("ell") ? 1 : 0);
  console.log(s.indexOf("l"));
  return 0;
}
