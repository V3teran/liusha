ALTER TABLE finding ALTER COLUMN class SET DEFAULT '';
COMMENT ON COLUMN finding.class IS '发现类型：坐实主张时评估者给出的主张类别（小写 snake_case，词表由场景定）；空 = 未给出';
