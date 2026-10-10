function main(): i32 {
  const s: string = "a,b;c,d";
  console.log(s.split(";").length);
  console.log(s.split(";")[1]);
  return 0;
}
